package main

import (
	"archive/zip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ddvk/rmapi-hwr/hwr/models"
	"github.com/juruen/rmapi/archive"
	"github.com/juruen/rmapi/encoding/rm"
)

// ContentFile represents the structure of the .content file in newer Remarkable formats
type ContentFile struct {
	CPages struct {
		Pages []struct {
			ID string `json:"id"`
		} `json:"pages"`
		LastOpened struct {
			Value string `json:"value"`
		} `json:"lastOpened"`
	} `json:"cPages"`
}

// parseRmVersion6 parses version 6 .rm files and converts them to the internal format
func parseRmVersion6(data []byte) (*rm.Rm, error) {
	if len(data) < 43 {
		return nil, fmt.Errorf("file too short")
	}

	header := string(data[0:43])
	if !strings.Contains(header, "version=6") {
		return nil, fmt.Errorf("not a version 6 file")
	}

	pos := 43 // Skip header

	// Skip initial metadata (5 bytes)
	if pos+5 > len(data) {
		return nil, fmt.Errorf("unexpected end of file")
	}
	pos += 5

	// Skip flags (5 bytes)
	if pos+5 > len(data) {
		return nil, fmt.Errorf("unexpected end of file")
	}
	pos += 5

	// Read layer count
	if pos+4 > len(data) {
		return nil, fmt.Errorf("unexpected end of file")
	}
	numLayers := binary.LittleEndian.Uint32(data[pos : pos+4])
	pos += 4

	// Skip UUID (16 bytes)
	if pos+16 > len(data) {
		return nil, fmt.Errorf("unexpected end of file")
	}
	pos += 16

	// Skip more metadata (looks like 7 bytes based on hexdump)
	if pos+7 > len(data) {
		return nil, fmt.Errorf("unexpected end of file")
	}
	pos += 7

	rmData := rm.New()
	rmData.Layers = make([]rm.Layer, numLayers)

	// Parse each layer
	for layerIdx := uint32(0); layerIdx < numLayers; layerIdx++ {
		var lines []rm.Line

		// Look for "Layer" string to find layer boundaries
		layerNamePos := -1
		for i := pos; i < len(data)-10; i++ {
			if i+7 < len(data) && string(data[i:i+7]) == "Layer " {
				layerNamePos = i
				break
			}
		}

		parseStart := pos
		parseEnd := len(data)
		if layerNamePos > 0 && layerIdx < numLayers-1 {
			parseEnd = layerNamePos
		}

		linePos := parseStart
		for linePos < parseEnd-50 {
			savedPos := linePos

			if linePos+4 > parseEnd {
				break
			}
			brushType := binary.LittleEndian.Uint32(data[linePos : linePos+4])
			linePos += 4

			if brushType > 50 {
				linePos = savedPos + 1
				continue
			}

			if linePos+4 > parseEnd {
				break
			}
			brushColor := binary.LittleEndian.Uint32(data[linePos : linePos+4])
			linePos += 4

			if linePos+4 > parseEnd {
				break
			}
			padding := binary.LittleEndian.Uint32(data[linePos : linePos+4])
			linePos += 4

			if linePos+4 > parseEnd {
				break
			}
			brushSizeBits := binary.LittleEndian.Uint32(data[linePos : linePos+4])
			brushSize := math.Float32frombits(brushSizeBits)
			linePos += 4

			if brushSize < 0 || brushSize > 100 {
				linePos = savedPos + 1
				continue
			}

			if linePos+4 > parseEnd {
				break
			}
			unknownBits := binary.LittleEndian.Uint32(data[linePos : linePos+4])
			unknown := math.Float32frombits(unknownBits)
			linePos += 4

			if linePos+4 > parseEnd {
				break
			}
			numPoints := binary.LittleEndian.Uint32(data[linePos : linePos+4])
			linePos += 4

			if numPoints == 0 || numPoints > 50000 {
				linePos = savedPos + 1
				continue
			}

			pointsNeeded := int(numPoints) * 24
			if linePos+pointsNeeded > parseEnd {
				linePos = savedPos + 1
				continue
			}

			line := rm.Line{
				BrushType:  rm.BrushType(brushType),
				BrushColor: rm.BrushColor(brushColor),
				Padding:    padding,
				BrushSize:  rm.BrushSize(brushSize),
				Unknown:    unknown,
				Points:     make([]rm.Point, numPoints),
			}

			pointsRead := 0
			for i := uint32(0); i < numPoints; i++ {
				if linePos+24 > parseEnd {
					break
				}

				x := math.Float32frombits(binary.LittleEndian.Uint32(data[linePos : linePos+4]))
				linePos += 4
				y := math.Float32frombits(binary.LittleEndian.Uint32(data[linePos : linePos+4]))
				linePos += 4
				speed := math.Float32frombits(binary.LittleEndian.Uint32(data[linePos : linePos+4]))
				linePos += 4
				direction := math.Float32frombits(binary.LittleEndian.Uint32(data[linePos : linePos+4]))
				linePos += 4
				width := math.Float32frombits(binary.LittleEndian.Uint32(data[linePos : linePos+4]))
				linePos += 4
				pressure := math.Float32frombits(binary.LittleEndian.Uint32(data[linePos : linePos+4]))
				linePos += 4

				if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) ||
					math.IsNaN(float64(y)) || math.IsInf(float64(y), 0) ||
					math.IsNaN(float64(speed)) || math.IsInf(float64(speed), 0) ||
					math.IsNaN(float64(direction)) || math.IsInf(float64(direction), 0) ||
					math.IsNaN(float64(width)) || math.IsInf(float64(width), 0) ||
					math.IsNaN(float64(pressure)) || math.IsInf(float64(pressure), 0) {
					continue
				}

				if x < -1000 || x > 20000 || y < -1000 || y > 20000 {
					continue
				}

				point := rm.Point{
					X:         x,
					Y:         y,
					Speed:     speed,
					Direction: direction,
					Width:     width,
					Pressure:  pressure,
				}

				line.Points[pointsRead] = point
				pointsRead++
			}

			if pointsRead > 0 {
				line.Points = line.Points[:pointsRead]
				lines = append(lines, line)
			} else {
				linePos = savedPos + 1
				continue
			}
		}

		rmData.Layers[layerIdx].Lines = lines

		if layerNamePos > 0 {
			nameEnd := layerNamePos + 7
			for nameEnd < len(data) && data[nameEnd] != 0 && data[nameEnd] != '<' && nameEnd < layerNamePos+20 {
				nameEnd++
			}
			pos = nameEnd
		} else {
			pos = linePos
		}
	}

	return rmData, nil
}

func loadRmZip(filename string) (zipArchive *archive.Zip, err error) {
	zipArchive = archive.NewZip()
	file, err := os.Open(filename)

	if err != nil {
		return
	}

	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		return
	}

	// Try the standard rmapi Read first
	err = zipArchive.Read(file, fi.Size())
	if err == nil {
		numPages := len(zipArchive.Pages)
		if numPages > 0 {
			return zipArchive, nil
		}
	}

	// If standard read failed or found no pages, try new format
	file.Seek(0, 0)
	return loadRmZipNewFormat(file)
}

func loadRmZipNewFormat(file *os.File) (zipArchive *archive.Zip, err error) {
	zipArchive = archive.NewZip()

	fi, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("can't stat file: %w", err)
	}

	file.Seek(0, 0)

	reader, err := zip.NewReader(file, fi.Size())
	if err != nil {
		return nil, fmt.Errorf("can't open as zip: %w", err)
	}

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
		return nil, errors.New("no .content file found in archive")
	}

	contentReader, err := contentFile.Open()
	if err != nil {
		return nil, fmt.Errorf("can't open content file: %w", err)
	}
	defer contentReader.Close()

	contentData, err := ioutil.ReadAll(contentReader)
	if err != nil {
		return nil, fmt.Errorf("can't read content file: %w", err)
	}

	var content ContentFile
	err = json.Unmarshal(contentData, &content)
	if err != nil {
		return nil, fmt.Errorf("can't parse content file: %w", err)
	}

	zipArchive.UUID = docUUID

	if len(content.CPages.Pages) > 0 {
		lastOpenedID := content.CPages.LastOpened.Value
		for i, page := range content.CPages.Pages {
			if page.ID == lastOpenedID {
				zipArchive.Content.LastOpenedPage = i
				break
			}
		}
	}

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

		pageData, err := ioutil.ReadAll(pageReader)
		pageReader.Close()
		if err != nil {
			log.Printf("Warning: can't read page file %s: %v", pagePath, err)
			continue
		}

		page := archive.Page{}

		if len(pageData) >= 43 && strings.Contains(string(pageData[0:43]), "version=6") {
			rmData, parseErr := parseRmVersion6(pageData)
			if parseErr != nil {
				log.Printf("Warning: can't parse version 6 page file %s: %v", pagePath, parseErr)
				continue
			}
			page.Data = rmData
		} else {
			page.Data = rm.New()
			err = page.Data.UnmarshalBinary(pageData)
			if err != nil {
				log.Printf("Warning: can't parse page file %s: %v", pagePath, err)
				continue
			}
		}

		zipArchive.Pages = append(zipArchive.Pages, page)
	}

	if len(zipArchive.Pages) == 0 {
		return nil, errors.New("no pages found in archive")
	}

	return zipArchive, nil
}

// generateJSON converts a Remarkable archive page to the JSON format sent to HWR service
func generateJSON(zip *archive.Zip, contenttype string, lang string, pageNumber int) ([]byte, error) {
	numPages := len(zip.Pages)

	if pageNumber >= numPages || pageNumber < 0 {
		return nil, fmt.Errorf("page %d outside range, max: %d", pageNumber, numPages)
	}

	batch := models.BatchInput{
		Configuration: &models.Configuration{
			Lang: lang,
		},
		StrokeGroups: []*models.StrokeGroup{
			&models.StrokeGroup{},
		},
		ContentType: &contenttype,
		Width:       14040,
		Height:      18720,
		XDPI:        2280,
		YDPI:        2280,
	}

	sg := batch.StrokeGroups[0]

	page := zip.Pages[pageNumber]

	if page.Data == nil {
		return nil, errors.New("no page content")
	}

	for _, layer := range page.Data.Layers {
		for _, line := range layer.Lines {
			pointerType := ""
			if line.BrushType == rm.EraseArea {
				continue
			}
			if line.BrushType == rm.Eraser {
				pointerType = "ERASER"
			}
			stroke := models.Stroke{
				X:           make([]float32, 0),
				Y:           make([]float32, 0),
				PointerType: pointerType,
			}
			sg.Strokes = append(sg.Strokes, &stroke)

			for _, point := range line.Points {
				x := point.X * 10
				y := point.Y * 10
				stroke.X = append(stroke.X, x)
				stroke.Y = append(stroke.Y, y)
			}
		}
	}

	return batch.MarshalBinary()
}

func main() {
	flag.Usage = func() {
		exec := os.Args[0]
		output := flag.CommandLine.Output()
		fmt.Fprintf(output, "Usage: %s [options] somefile.zip\n", exec)
		fmt.Fprintln(output, "\tConverts Remarkable ZIP file to JSON format sent to HWR service")
		fmt.Fprintln(output, "Options:")
		flag.PrintDefaults()
	}

	var inputType = flag.String("type", "Text", "type of the content: Text, Math, Diagram")
	var lang = flag.String("lang", "en_US", "language culture")
	var page = flag.Int("page", -1, "page to convert (default all pages)")
	var outputFile = flag.String("o", "", "output file (default stdout)")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		log.Fatal("no file specified")
	}

	filename := args[0]
	ext := path.Ext(filename)

	var err error
	var z *archive.Zip

	switch ext {
	case ".zip":
		z, err = loadRmZip(filename)
	default:
		log.Fatal("Unsupported file type. Expected .zip file")
	}

	if err != nil {
		log.Fatalln(err, "Can't read file ", filename)
	}

	numPages := len(z.Pages)
	if numPages == 0 {
		log.Fatal("no pages found in file")
	}

	var output *os.File
	if *outputFile == "" {
		output = os.Stdout
	} else {
		output, err = os.Create(*outputFile)
		if err != nil {
			log.Fatalf("can't create output file: %v", err)
		}
		defer output.Close()
	}

	// Determine which pages to convert
	pagesToConvert := []int{}
	if *page < 0 {
		// Convert all pages
		for i := 0; i < numPages; i++ {
			pagesToConvert = append(pagesToConvert, i)
		}
	} else {
		if *page > numPages {
			log.Fatalf("page %d out of range (max: %d)", *page, numPages-1)
		}
		pagesToConvert = append(pagesToConvert, *page-1) // Convert to 0-based index
	}

	// Convert each page
	for i, pageNum := range pagesToConvert {
		jsonData, err := generateJSON(z, *inputType, *lang, pageNum)
		if err != nil {
			log.Printf("Error converting page %d: %v", pageNum+1, err)
			continue
		}

		if len(pagesToConvert) > 1 {
			fmt.Fprintf(output, "=== Page %d ===\n", pageNum+1)
		}

		// Pretty print JSON
		var prettyJSON map[string]interface{}
		if err := json.Unmarshal(jsonData, &prettyJSON); err == nil {
			prettyBytes, err := json.MarshalIndent(prettyJSON, "", "  ")
			if err == nil {
				output.Write(prettyBytes)
			} else {
				output.Write(jsonData)
			}
		} else {
			output.Write(jsonData)
		}

		if i < len(pagesToConvert)-1 {
			output.WriteString("\n\n")
		}
	}
}

