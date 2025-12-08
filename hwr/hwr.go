package hwr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"golang.org/x/sync/semaphore"

	"github.com/ddvk/rmapi-hwr/hwr/client"
	"github.com/ddvk/rmapi-hwr/hwr/models"
	"github.com/juruen/rmapi/archive"
	"github.com/juruen/rmapi/encoding/rm"
)

var NoContent = errors.New("no page content")

type Config struct {
	Page           int
	applicationKey string
	hmacKey        string
	Lang           string
	InputType      string
	OutputType     string
	OutputFile     string
	AddPages       bool
	BatchSize      int64
}

func getJson(zip *archive.Zip, contenttype string, lang string, pageNumber int) (r []byte, err error) {
	numPages := len(zip.Pages)

	if pageNumber >= numPages || pageNumber < 0 {
		err = fmt.Errorf("page %d outside range, max: %d", pageNumber, numPages)
		return
	}

	batch := models.BatchInput{
		Configuration: &models.Configuration{
			Lang: lang,
		},
		StrokeGroups: []*models.StrokeGroup{
			&models.StrokeGroup{},
		},
		ContentType: &contenttype,
		Width:       1404,  // Remarkable2 screen width in pixels
		Height:      1872,  // Remarkable2 screen height in pixels
		XDPI:        226,   // Remarkable2 DPI
		YDPI:        226,   // Remarkable2 DPI
	}

	sg := batch.StrokeGroups[0]

	page := zip.Pages[pageNumber]

	if page.Data == nil {
		return nil, NoContent
	}

	log.Printf("Page %d: Found %d layers", pageNumber, len(page.Data.Layers))
	totalLines := 0
	totalPoints := 0
	
	for _, layer := range page.Data.Layers {
		for _, line := range layer.Lines {
			totalLines++
			totalPoints += len(line.Points)
			
			// Skip erase area strokes
			if line.BrushType == rm.EraseArea {
				continue
			}
			
			// Skip empty lines
			if len(line.Points) == 0 {
				continue
			}
			
			// Set pointer type - default to PEN, ERASER for eraser strokes
			pointerType := "PEN"
			if line.BrushType == rm.Eraser {
				pointerType = "ERASER"
			}
			
			// Create stroke and populate points first
			stroke := models.Stroke{
				X:           make([]float32, 0, len(line.Points)),
				Y:           make([]float32, 0, len(line.Points)),
				P:           make([]float32, 0, len(line.Points)), // Pressure
				T:           make([]int64, 0, len(line.Points)),   // Timestamps
				PointerType: pointerType,
			}

			// Use a timestamp counter for relative timing (in milliseconds)
			timestamp := int64(0)
			for _, point := range line.Points {
				// Remarkable coordinates are already in pixels, no scaling needed
				x := point.X
				y := point.Y
				stroke.X = append(stroke.X, x)
				stroke.Y = append(stroke.Y, y)
				// Add pressure (normalize to 0-1 range if needed)
				pressure := float32(point.Pressure)
				if pressure <= 0 {
					// Default pressure if not available
					pressure = 0.5
				} else if pressure > 1.0 {
					// Normalize if pressure is in a different range
					pressure = pressure / 10.0
					if pressure > 1.0 {
						pressure = 1.0
					}
				}
				stroke.P = append(stroke.P, pressure)
				// Add timestamp (increment by 16ms per point, typical sampling rate ~60Hz)
				stroke.T = append(stroke.T, timestamp)
				timestamp += 16
			}
			
			// Only append stroke if it has points
			if len(stroke.X) > 0 && len(stroke.Y) > 0 {
				sg.Strokes = append(sg.Strokes, &stroke)
			}
		}
	}
	
	log.Printf("Page %d: Processed %d lines with %d total points, created %d strokes", 
		pageNumber, totalLines, totalPoints, len(sg.Strokes))

	// Debug: Log coordinate ranges
	if len(sg.Strokes) > 0 {
		minX, maxX := float32(999999), float32(-999999)
		minY, maxY := float32(999999), float32(-999999)
		for _, stroke := range sg.Strokes {
			if stroke != nil {
				for _, x := range stroke.X {
					if x < minX { minX = x }
					if x > maxX { maxX = x }
				}
				for _, y := range stroke.Y {
					if y < minY { minY = y }
					if y > maxY { maxY = y }
				}
			}
		}
		log.Printf("Page %d: Coordinate ranges - X: [%.2f, %.2f], Y: [%.2f, %.2f], Canvas: [%d, %d]", 
			pageNumber, minX, maxX, minY, maxY, batch.Width, batch.Height)
	}

	r, err = batch.MarshalBinary()
	if err != nil {
		return
	}
	
	// Debug: Save JSON to file for inspection
	if pageNumber == 0 {
		debugFile := fmt.Sprintf("/tmp/hwr_debug_page_%d.json", pageNumber)
		if err := os.WriteFile(debugFile, r, 0644); err == nil {
			log.Printf("Page %d: Saved request JSON to %s for debugging", pageNumber, debugFile)
		}
	}
	
	return
}

func Hwr(zip *archive.Zip, cfg Config) {
	applicationKey := os.Getenv("RMAPI_HWR_APPLICATIONKEY")
	if applicationKey == "" {
		log.Fatal("provide the myScript applicationKey in: RMAPI_HWR_APPLICATIONKEY")
	}
	hmacKey := os.Getenv("RMAPI_HWR_HMAC")
	if hmacKey == "" {
		log.Fatal("provide the myScript hmac in: RMAPI_HWR_HMAC")
	}

	capacity := 1
	start := 0
	var end int

	if cfg.Page == 0 {
		start = zip.Content.LastOpenedPage
		end = start
	} else if cfg.Page < 0 {
		capacity = len(zip.Pages)
		end = capacity - 1
	} else {
		start = cfg.Page - 1
		end = start
	}
	result := make([][]byte, capacity)

	contenttype, output := setContentType(cfg.InputType)

	ctx := context.TODO()
	sem := semaphore.NewWeighted(cfg.BatchSize)
	for p := start; p <= end; p++ {
		log.Println("Page: ", p)
		if err := sem.Acquire(ctx, 1); err != nil {
			log.Printf("Failed to acquire semaphore: %v", err)
			break
		}
		go func(p int) {
			defer sem.Release(1)
			js, err := getJson(zip, contenttype, cfg.Lang, p)
			if err != nil {
				log.Fatalf("Can't get page: %d %v\n", p, err)
			}
			
			// Debug: Log JSON structure info
			var debugBatch models.BatchInput
			if err := json.Unmarshal(js, &debugBatch); err == nil {
				totalStrokes := 0
				totalPoints := 0
				for _, sg := range debugBatch.StrokeGroups {
					if sg != nil {
						totalStrokes += len(sg.Strokes)
						for _, stroke := range sg.Strokes {
							if stroke != nil {
								if len(stroke.X) > totalPoints {
									totalPoints = len(stroke.X)
								}
							}
						}
					}
				}
				log.Printf("Page %d: Prepared JSON with %d stroke groups, %d total strokes, max %d points per stroke", 
					p, len(debugBatch.StrokeGroups), totalStrokes, totalPoints)
				if totalStrokes == 0 {
					log.Printf("WARNING: Page %d has no strokes! JSON size: %d bytes", p, len(js))
				}
			}
			
			log.Println("sending request: ", p)

			body, err := client.SendRequest(applicationKey, hmacKey, js, output)
			if err != nil {
				if body != nil {
					log.Println(string(body))
				}
				log.Fatal(err)
			}
			
			// Debug: Log response info
			if len(body) > 0 {
				previewLen := min(200, len(body))
				log.Printf("Page %d: Received response (%d bytes), first %d chars: %q", 
					p, len(body), previewLen, string(body[:previewLen]))
				if len(body) > 0 && body[0] == '{' {
					log.Printf("Page %d: Response appears to be JSON (Jiix format)", p)
					// Try to pretty print first part of JSON
					var jsonPreview map[string]interface{}
					if err := json.Unmarshal(body, &jsonPreview); err == nil {
						keys := getMapKeys(jsonPreview)
						log.Printf("Page %d: JSON keys: %v", p, keys)
					}
				} else {
					log.Printf("Page %d: Response appears to be plain text (content: %q)", p, string(body))
				}
			} else {
				log.Printf("Page %d: Received empty response!", p)
			}
			
			result[p] = body
			log.Println("converted page ", p)
		}(p)
	}
	log.Println("wating for all to finish")
	if err := sem.Acquire(ctx, cfg.BatchSize); err != nil {
		log.Printf("Failed to acquire semaphore: %v", err)
	}

	if cfg.OutputFile == "-" {
		dump(result, cfg.AddPages)
	} else {
		//text file
		f, err := os.Create(cfg.OutputFile + ".txt")
		if err != nil {
			dump(result, cfg.AddPages)
			log.Fatal(err)
		}

		for _, c := range result {
			text := extractTextFromResponse(c, output)
			f.WriteString(text)
			f.Write([]byte("\n"))
		}
		f.Close()
	}
}

func dump(result [][]byte, addPages bool) {
	for p, c := range result {
		if addPages {
			fmt.Printf("=== Page %d ===\n", p)

		}
		// Try to extract text from response (might be Jiix JSON)
		text := extractTextFromResponse(c, "text/plain")
		fmt.Println(text)
	}
}

// extractTextFromResponse extracts text from HWR API response
// The response might be plain text or Jiix JSON format
func extractTextFromResponse(data []byte, expectedMimeType string) string {
	if len(data) == 0 {
		return ""
	}

	// Trim whitespace
	data = bytes.TrimSpace(data)
	
	// Check if response is JSON (Jiix format) - look for JSON start
	if len(data) > 0 && (data[0] == '{' || data[0] == '[') {
		text := extractTextFromJiix(data)
		if text != string(data) {
			// Successfully extracted text from JSON
			return text
		}
		// If extraction failed, try to parse as JSON anyway
		log.Printf("Warning: Failed to extract text from JSON, trying direct parse")
	}

	// If it's supposed to be plain text, return as-is
	if expectedMimeType == "text/plain" {
		return string(data)
	}

	// For other formats, return as string
	return string(data)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// extractTextFromJiix extracts text from Jiix JSON format
func extractTextFromJiix(data []byte) string {
	// Try to parse as JSON object first
	var jiix map[string]interface{}
	if err := json.Unmarshal(data, &jiix); err == nil {
		return extractTextFromJiixObject(jiix)
	}
	
	// Try to parse as JSON array
	var jiixArray []interface{}
	if err := json.Unmarshal(data, &jiixArray); err == nil {
		var textParts []string
		for _, item := range jiixArray {
			if itemMap, ok := item.(map[string]interface{}); ok {
				text := extractTextFromJiixObject(itemMap)
				if text != "" {
					textParts = append(textParts, text)
				}
			}
		}
		if len(textParts) > 0 {
			return strings.Join(textParts, " ")
		}
	}
	
	// Not valid JSON, return as string
	log.Printf("Warning: Response is not valid JSON, first 100 bytes: %s", string(data[:min(100, len(data))]))
	return string(data)
}

func extractTextFromJiixObject(jiix map[string]interface{}) string {
	var textParts []string

	// Try to extract from "text" field (direct text output)
	if textField, ok := jiix["text"].(string); ok && textField != "" {
		return textField
	}

	// Try to extract from "label" field (direct label)
	if label, ok := jiix["label"].(string); ok && label != "" {
		return label
	}

	// Try to extract from "words" array (most common in Jiix)
	if words, ok := jiix["words"].([]interface{}); ok {
		for _, word := range words {
			if wordMap, ok := word.(map[string]interface{}); ok {
				// Try "label" field first
				if label, ok := wordMap["label"].(string); ok && label != "" {
					textParts = append(textParts, label)
				} else if text, ok := wordMap["text"].(string); ok && text != "" {
					textParts = append(textParts, text)
				}
			} else if wordStr, ok := word.(string); ok {
				textParts = append(textParts, wordStr)
			}
		}
		if len(textParts) > 0 {
			return strings.Join(textParts, " ")
		}
	}

	// Try to extract from "chars" array (character-level recognition)
	if chars, ok := jiix["chars"].([]interface{}); ok {
		for _, char := range chars {
			if charMap, ok := char.(map[string]interface{}); ok {
				if label, ok := charMap["label"].(string); ok && label != "" {
					textParts = append(textParts, label)
				} else if text, ok := charMap["text"].(string); ok && text != "" {
					textParts = append(textParts, text)
				}
			} else if charStr, ok := char.(string); ok {
				textParts = append(textParts, charStr)
			}
		}
		if len(textParts) > 0 {
			return strings.Join(textParts, "")
		}
	}

	// Try to extract from "items" array (alternative structure)
	if items, ok := jiix["items"].([]interface{}); ok {
		for _, item := range items {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if itemType, ok := itemMap["type"].(string); ok && itemType == "text" {
					if label, ok := itemMap["label"].(string); ok && label != "" {
						textParts = append(textParts, label)
					} else if text, ok := itemMap["text"].(string); ok && text != "" {
						textParts = append(textParts, text)
					}
				}
			}
		}
		if len(textParts) > 0 {
			return strings.Join(textParts, " ")
		}
	}
	
	// Try to extract from "result" field (some APIs wrap the response)
	if result, ok := jiix["result"]; ok {
		if resultMap, ok := result.(map[string]interface{}); ok {
			text := extractTextFromJiixObject(resultMap)
			if text != "" {
				return text
			}
		}
	}

	// If we can't parse it, return empty string (will fall back to raw data)
	log.Printf("Warning: Could not extract text from Jiix format, available keys: %v", getMapKeys(jiix))
	return ""
}

func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
func setContentType(requested string) (contenttype string, output string) {
	switch strings.ToLower(requested) {
	case "math":
		contenttype = "Math"
		output = "application/x-latex"
	case "text":
		contenttype = "Text"
		output = "text/plain"
	case "diagram":
		contenttype = "Diagram"
		output = "image/svg+xml"
	case "jiix":
		contenttype = "Text"
		output = "application/vnd.myscript.jiix"
	default:
		log.Fatal("unsupported content type: " + contenttype)
	}
	return
}
