package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ddvk/rmapi-hwr/hwr"
	"github.com/ddvk/rmapi-hwr/hwr/client"
	"github.com/ddvk/rmapi-hwr/hwr/models"
	"github.com/juruen/rmapi/archive"
	"github.com/juruen/rmapi/encoding/rm"
)

const (
	defaultPort = "8082"
	maxFileSize = 100 * 1024 * 1024 // 100MB
)

type Server struct {
	port           string
	outputDir      string
	applicationKey string
	hmacKey        string
}

func NewServer() *Server {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	outputDir := os.Getenv("OUTPUT_DIR")
	if outputDir == "" {
		outputDir = "/tmp/rmapi-hwr-output"
	}

	applicationKey := os.Getenv("RMAPI_HWR_APPLICATIONKEY")
	hmacKey := os.Getenv("RMAPI_HWR_HMAC")

	return &Server{
		port:           port,
		outputDir:      outputDir,
		applicationKey: applicationKey,
		hmacKey:        hmacKey,
	}
}

func (s *Server) loadRmZip(file io.ReaderAt, size int64) (*archive.Zip, error) {
	zipArchive := archive.NewZip()
	err := zipArchive.Read(file, size)
	if err == nil {
		numPages := len(zipArchive.Pages)
		log.Printf("Standard rmapi Read() succeeded: found %d pages", numPages)
		if numPages > 0 {
			// Check if pages have data
			pagesWithData := 0
			for i, page := range zipArchive.Pages {
				if page.Data != nil {
					layers := len(page.Data.Layers)
					totalLines := 0
					for _, layer := range page.Data.Layers {
						totalLines += len(layer.Lines)
					}
					log.Printf("Page %d: %d layers, %d lines", i, layers, totalLines)
					if layers > 0 && totalLines > 0 {
						pagesWithData++
					}
				}
			}
			log.Printf("Pages with data: %d/%d", pagesWithData, numPages)
			if pagesWithData > 0 {
				return zipArchive, nil
			} else {
				log.Printf("Standard parser found pages but no data, trying new format parser")
			}
		} else {
			log.Printf("Standard parser found 0 pages, trying new format parser")
		}
	} else {
		log.Printf("Standard rmapi Read() failed: %v, trying new format parser", err)
	}

	// Try new format parser
	reader, err := zip.NewReader(file, size)
	if err != nil {
		return nil, fmt.Errorf("can't open as zip: %w", err)
	}

	return s.loadRmZipNewFormat(reader)
}

func (s *Server) loadRmZipNewFormat(reader *zip.Reader) (*archive.Zip, error) {
	zipArchive := archive.NewZip()

	// Find the .content file to get page list
	var contentFile *zip.File
	var docUUID string
	for _, f := range reader.File {
		if strings.HasSuffix(f.Name, ".content") {
			contentFile = f
			baseName := filepath.Base(f.Name)
			docUUID = strings.TrimSuffix(baseName, ".content")
			break
		}
	}

	if contentFile == nil {
		return nil, fmt.Errorf("no .content file found in archive")
	}

	// Read and parse content file
	contentReader, err := contentFile.Open()
	if err != nil {
		return nil, fmt.Errorf("can't open content file: %w", err)
	}
	defer contentReader.Close()

	contentData, err := io.ReadAll(contentReader)
	if err != nil {
		return nil, fmt.Errorf("can't read content file: %w", err)
	}

	var content struct {
		CPages struct {
			Pages []struct {
				ID string `json:"id"`
			} `json:"pages"`
			LastOpened struct {
				Value string `json:"value"`
			} `json:"lastOpened"`
		} `json:"cPages"`
	}

	err = json.Unmarshal(contentData, &content)
	if err != nil {
		return nil, fmt.Errorf("can't parse content file: %w", err)
	}

	// Set UUID
	zipArchive.UUID = docUUID

	// Set Content metadata if available
	if len(content.CPages.Pages) > 0 {
		// Find last opened page index
		lastOpenedID := content.CPages.LastOpened.Value
		for i, page := range content.CPages.Pages {
			if page.ID == lastOpenedID {
				zipArchive.Content.LastOpenedPage = i
				break
			}
		}
	}

	// Read each page .rm file
	for _, pageInfo := range content.CPages.Pages {
		pageID := pageInfo.ID
		pagePath := fmt.Sprintf("%s/%s.rm", docUUID, pageID)

		var pageFile *zip.File
		for _, f := range reader.File {
			if f.Name == pagePath {
				pageFile = f
				break
			}
		}

		if pageFile == nil {
			log.Printf("Warning: page file not found: %s", pagePath)
			continue
		}

		pageReader, err := pageFile.Open()
		if err != nil {
			log.Printf("Warning: can't open page file %s: %v", pagePath, err)
			continue
		}

		pageData, err := io.ReadAll(pageReader)
		pageReader.Close()
		if err != nil {
			log.Printf("Warning: can't read page file %s: %v", pagePath, err)
			continue
		}

		page := archive.Page{}
		page.Data = rm.New()
		err = page.Data.UnmarshalBinary(pageData)
		if err != nil {
			log.Printf("Warning: can't parse page file %s: %v", pagePath, err)
			continue
		}

		zipArchive.Pages = append(zipArchive.Pages, page)
	}

	if len(zipArchive.Pages) == 0 {
		return nil, fmt.Errorf("no pages found in archive")
	}

	return zipArchive, nil
}

func (s *Server) handleHWR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form
	err := r.ParseMultipartForm(maxFileSize)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error parsing form: %v", err), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting file: %v", err), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Read file into memory
	fileData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error reading file: %v", err), http.StatusInternalServerError)
		return
	}

	// Get optional parameters
	inputType := r.FormValue("type")
	if inputType == "" {
		inputType = "Text"
	}
	lang := r.FormValue("lang")
	if lang == "" {
		lang = "en_US"
	}
	pageStr := r.FormValue("page")
	page := -1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil {
			page = p
		}
	}

	// Load the zip archive
	reader := bytes.NewReader(fileData)
	zipArchive, err := s.loadRmZip(reader, int64(len(fileData)))
	if err != nil {
		http.Error(w, fmt.Sprintf("Error loading rmdoc: %v", err), http.StatusBadRequest)
		return
	}

	// Check if HWR credentials are available
	if s.applicationKey == "" || s.hmacKey == "" {
		http.Error(w, "HWR credentials not configured", http.StatusInternalServerError)
		return
	}

	// Set environment variables for HWR
	os.Setenv("RMAPI_HWR_APPLICATIONKEY", s.applicationKey)
	os.Setenv("RMAPI_HWR_HMAC", s.hmacKey)

	// Configure HWR
	cfg := hwr.Config{
		Page:      page,
		Lang:      lang,
		InputType: inputType,
		AddPages:  true,
		BatchSize: 3,
	}

	// Process HWR
	result := s.processHWR(zipArchive, cfg)
	if len(result) == 0 {
		http.Error(w, "No content found", http.StatusNotFound)
		return
	}

	// Return result as JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"filename": header.Filename,
		"pages":    len(zipArchive.Pages),
		"text":     result,
	})
}

func (s *Server) processHWR(zipArchive *archive.Zip, cfg hwr.Config) map[int]string {
	start := 0
	var end int

	if cfg.Page == 0 {
		start = zipArchive.Content.LastOpenedPage
		end = start
	} else if cfg.Page < 0 {
		end = len(zipArchive.Pages) - 1
	} else {
		start = cfg.Page - 1
		end = start
	}

	result := make(map[int]string)

	for p := start; p <= end; p++ {
		js, err := s.buildBatchInput(zipArchive, cfg.InputType, cfg.Lang, p)
		if err != nil {
			log.Printf("Error building batch input for page %d: %v", p, err)
			continue
		}

		body, err := client.SendRequest(s.applicationKey, s.hmacKey, js, "text/plain")
		if err != nil {
			log.Printf("Error sending HWR request for page %d: %v", p, err)
			continue
		}

		text := s.extractTextFromResponse(body)
		if text != "" {
			result[p] = text
		}
	}

	return result
}

func (s *Server) buildBatchInput(zipArchive *archive.Zip, contentType, lang string, pageNumber int) ([]byte, error) {
	if pageNumber < 0 || pageNumber >= len(zipArchive.Pages) {
		return nil, fmt.Errorf("page %d outside range", pageNumber)
	}

	page := zipArchive.Pages[pageNumber]
	if page.Data == nil {
		return nil, fmt.Errorf("no data for page %d", pageNumber)
	}

	batch := models.BatchInput{
		Configuration: &models.Configuration{
			Lang: lang,
		},
		StrokeGroups: []*models.StrokeGroup{
			{},
		},
		ContentType: &contentType,
		Width:       1404,
		Height:      1872,
		XDPI:        226,
		YDPI:        226,
	}

	sg := batch.StrokeGroups[0]

	for _, layer := range page.Data.Layers {
		for _, line := range layer.Lines {
			if line.BrushType == rm.EraseArea || len(line.Points) == 0 {
				continue
			}

			pointerType := "PEN"
			if line.BrushType == rm.Eraser {
				pointerType = "ERASER"
			}

			stroke := models.Stroke{
				X:           make([]float32, 0, len(line.Points)),
				Y:           make([]float32, 0, len(line.Points)),
				P:           make([]float32, 0, len(line.Points)),
				T:           make([]int64, 0, len(line.Points)),
				PointerType: pointerType,
			}

			timestamp := int64(0)
			for _, point := range line.Points {
				stroke.X = append(stroke.X, point.X)
				stroke.Y = append(stroke.Y, point.Y)
				pressure := float32(point.Pressure)
				if pressure <= 0 {
					pressure = 0.5
				} else if pressure > 1.0 {
					pressure = pressure / 10.0
					if pressure > 1.0 {
						pressure = 1.0
					}
				}
				stroke.P = append(stroke.P, pressure)
				stroke.T = append(stroke.T, timestamp)
				timestamp += 16
			}

			if len(stroke.X) > 0 && len(stroke.Y) > 0 {
				sg.Strokes = append(sg.Strokes, &stroke)
			}
		}
	}

	return batch.MarshalBinary()
}

func (s *Server) extractTextFromResponse(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	data = bytes.TrimSpace(data)

	// Check if response is JSON (Jiix format)
	if len(data) > 0 && (data[0] == '{' || data[0] == '[') {
		var jiix map[string]interface{}
		if err := json.Unmarshal(data, &jiix); err == nil {
			return s.extractTextFromJiix(jiix)
		}
	}

	// Return as plain text
	return string(data)
}

func (s *Server) extractTextFromJiix(jiix map[string]interface{}) string {
	var textParts []string

	// Try to extract from "text" field
	if textField, ok := jiix["text"].(string); ok && textField != "" {
		return textField
	}

	// Try to extract from "words" array
	if words, ok := jiix["words"].([]interface{}); ok {
		for _, word := range words {
			if wordMap, ok := word.(map[string]interface{}); ok {
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

	// Try to extract from "chars" array
	if chars, ok := jiix["chars"].([]interface{}); ok {
		for _, char := range chars {
			if charMap, ok := char.(map[string]interface{}); ok {
				if label, ok := charMap["label"].(string); ok && label != "" {
					textParts = append(textParts, label)
				}
			}
		}
		if len(textParts) > 0 {
			return strings.Join(textParts, "")
		}
	}

	return ""
}

func (s *Server) handleConvert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form
	err := r.ParseMultipartForm(maxFileSize)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error parsing form: %v", err), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting file: %v", err), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Read file into memory
	fileData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error reading file: %v", err), http.StatusInternalServerError)
		return
	}

	// Get optional page parameter
	pageStr := r.FormValue("page")
	page := -1
	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil {
			page = p
		}
	}

	// Load the zip archive
	reader := bytes.NewReader(fileData)
	zipArchive, err := s.loadRmZip(reader, int64(len(fileData)))
	if err != nil {
		http.Error(w, fmt.Sprintf("Error loading rmdoc: %v", err), http.StatusBadRequest)
		return
	}

	// Create temporary directory for PNGs
	tempDir, err := os.MkdirTemp(s.outputDir, "convert-*")
	if err != nil {
		http.Error(w, fmt.Sprintf("Error creating temp dir: %v", err), http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tempDir)

	// Determine which pages to convert
	pagesToConvert := []int{}
	if page >= 0 {
		pagesToConvert = []int{page - 1} // Convert to 0-based
	} else {
		for i := 0; i < len(zipArchive.Pages); i++ {
			pagesToConvert = append(pagesToConvert, i)
		}
	}

	// Convert pages to PNG
	pngFiles := []string{}
	for _, p := range pagesToConvert {
		if p < 0 || p >= len(zipArchive.Pages) {
			log.Printf("Skipping invalid page index %d (total pages: %d)", p, len(zipArchive.Pages))
			continue
		}

		// Check if page has data
		page := zipArchive.Pages[p]
		if page.Data == nil {
			log.Printf("Page %d has no data, skipping", p)
			continue
		}

		// Check if page has any strokes
		hasStrokes := false
		for _, layer := range page.Data.Layers {
			if len(layer.Lines) > 0 {
				hasStrokes = true
				break
			}
		}

		if !hasStrokes {
			log.Printf("Page %d has no strokes, skipping", p)
			continue
		}

		outputPNG := filepath.Join(tempDir, fmt.Sprintf("page_%d.png", p))
		log.Printf("Converting page %d to PNG: %s", p, outputPNG)
		err := hwr.VisualizePage(zipArchive, p, outputPNG)
		if err != nil {
			log.Printf("Error visualizing page %d: %v", p, err)
			continue
		}

		// Verify PNG was created and is not empty
		// Add a small delay to ensure file is fully written
		info, err := os.Stat(outputPNG)
		if err != nil {
			log.Printf("Warning: Cannot stat PNG file for page %d: %v", p, err)
			continue
		}
		if info.Size() == 0 {
			log.Printf("Warning: PNG file for page %d is empty", p)
			continue
		}

		// Verify it's a valid PNG by trying to decode it
		pngFile, err := os.Open(outputPNG)
		if err != nil {
			log.Printf("Warning: Cannot open PNG file for page %d: %v", p, err)
			continue
		}
		// Try to decode the PNG to verify it's valid
		_, err = png.Decode(pngFile)
		pngFile.Close()
		if err != nil {
			log.Printf("Warning: PNG file for page %d is not valid (decode error: %v)", p, err)
			continue
		}

		pngFiles = append(pngFiles, outputPNG)
		log.Printf("Successfully converted page %d: %d bytes (valid PNG)", p, info.Size())
	}

	if len(pngFiles) == 0 {
		http.Error(w, "No pages converted", http.StatusInternalServerError)
		return
	}

	// Create a zip file with all PNGs
	zipBuffer := new(bytes.Buffer)
	zipWriter := zip.NewWriter(zipBuffer)

	for _, pngFile := range pngFiles {
		file, err := os.Open(pngFile)
		if err != nil {
			log.Printf("Error opening PNG file %s: %v", pngFile, err)
			continue
		}

		// Get file info for logging
		fileInfo, err := file.Stat()
		if err != nil {
			file.Close()
			log.Printf("Error statting PNG file %s: %v", pngFile, err)
			continue
		}

		zipEntry, err := zipWriter.Create(filepath.Base(pngFile))
		if err != nil {
			file.Close()
			log.Printf("Error creating zip entry for %s: %v", pngFile, err)
			continue
		}

		bytesWritten, err := io.Copy(zipEntry, file)
		file.Close()
		if err != nil {
			log.Printf("Error copying PNG file %s to zip: %v", pngFile, err)
			continue
		}

		if bytesWritten != fileInfo.Size() {
			log.Printf("Warning: Size mismatch for %s: expected %d, wrote %d", pngFile, fileInfo.Size(), bytesWritten)
		} else {
			log.Printf("Added %s to zip: %d bytes", filepath.Base(pngFile), bytesWritten)
		}
	}

	err = zipWriter.Close()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error creating zip: %v", err), http.StatusInternalServerError)
		return
	}

	// Return zip file
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s_pages.zip", strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))))
	w.Write(zipBuffer.Bytes())
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	})
}

func (s *Server) Start() error {
	// Ensure output directory exists
	err := os.MkdirAll(s.outputDir, 0755)
	if err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	http.HandleFunc("/api/hwr", s.handleHWR)
	http.HandleFunc("/api/convert", s.handleConvert)
	http.HandleFunc("/health", s.handleHealth)

	log.Printf("Server starting on port %s", s.port)
	return http.ListenAndServe(":"+s.port, nil)
}

func main() {
	server := NewServer()
	if err := server.Start(); err != nil {
		log.Fatal(err)
	}
}

