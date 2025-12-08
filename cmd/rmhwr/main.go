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

	"github.com/ddvk/rmapi-hwr/hwr"
	"github.com/juruen/rmapi/archive"
	"github.com/juruen/rmapi/encoding/rm"
)

func loadRmPage(filename string) (zip *archive.Zip, err error) {
	zip = archive.NewZip()
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	pageData, err := ioutil.ReadAll(file)

	if err != nil {
		log.Fatal("cant read fil")
		return
	}
	page := archive.Page{}
	page.Data = rm.New()
	err = page.Data.UnmarshalBinary(pageData)
	if err != nil {
		return nil, err
	}

	zip.Pages = append(zip.Pages, page)

	return zip, nil

}

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
	
	// Version 6 format structure (based on analysis):
	// - Header: 43 bytes
	// - Metadata: 5 bytes (version info)
	// - Flags: 5 bytes
	// - Layer count: 4 bytes (uint32)
	// - UUID: 16 bytes
	// - Then layers with names and data
	
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
	
	// Version 6 format: After UUID, there's layer metadata, then layer data
	// Each layer has: metadata block, then lines
	// Lines appear before "Layer N" strings in the data
	
	// Skip initial metadata after UUID - looks like there's variable-length metadata
	// Try to find where actual line data starts by looking for patterns
	
	// Parse each layer
	for layerIdx := uint32(0); layerIdx < numLayers; layerIdx++ {
		var lines []rm.Line
		
		// Try to find "Layer N" string to mark layer boundaries
		layerNamePos := -1
		for i := pos; i < len(data)-10; i++ {
			if i+7 < len(data) && string(data[i:i+7]) == "Layer " {
				layerNamePos = i
				break
			}
		}
		
		// If we found a layer name, parse lines before it
		// Otherwise, try to parse from current position
		parseStart := pos
		parseEnd := len(data)
		if layerNamePos > 0 && layerIdx < numLayers-1 {
			// There's another layer after this one
			parseEnd = layerNamePos
		}
		
		// Try to parse lines from parseStart to parseEnd
		linePos := parseStart
		for linePos < parseEnd-50 { // Need at least 50 bytes for a line with points
			savedPos := linePos
			
			// Try to read brush type (uint32)
			if linePos+4 > parseEnd {
				break
			}
			brushType := binary.LittleEndian.Uint32(data[linePos : linePos+4])
			linePos += 4
			
			// Brush types are typically 0-15, but let's be more lenient
			if brushType > 50 {
				linePos = savedPos + 1 // Try next byte
				continue
			}
			
			// Try to read brush color
			if linePos+4 > parseEnd {
				break
			}
			brushColor := binary.LittleEndian.Uint32(data[linePos : linePos+4])
			linePos += 4
			
			// Try to read padding
			if linePos+4 > parseEnd {
				break
			}
			padding := binary.LittleEndian.Uint32(data[linePos : linePos+4])
			linePos += 4
			
			// Try to read brush size (float32)
			if linePos+4 > parseEnd {
				break
			}
			brushSizeBits := binary.LittleEndian.Uint32(data[linePos : linePos+4])
			brushSize := math.Float32frombits(brushSizeBits)
			linePos += 4
			
			// Validate brush size is reasonable (typically 0.1 to 50.0)
			if brushSize < 0 || brushSize > 100 {
				linePos = savedPos + 1
				continue
			}
			
			// Try to read unknown field
			if linePos+4 > parseEnd {
				break
			}
			unknownBits := binary.LittleEndian.Uint32(data[linePos : linePos+4])
			unknown := math.Float32frombits(unknownBits)
			linePos += 4
			
			// Try to read number of points
			if linePos+4 > parseEnd {
				break
			}
			numPoints := binary.LittleEndian.Uint32(data[linePos : linePos+4])
			linePos += 4
			
			// Validate numPoints is reasonable
			if numPoints == 0 || numPoints > 50000 {
				linePos = savedPos + 1
				continue
			}
			
			// Try to read points - each point is 24 bytes (X, Y, Speed, Direction, Width, Pressure)
			pointsNeeded := int(numPoints) * 24
			if linePos+pointsNeeded > parseEnd {
				linePos = savedPos + 1
				continue
			}
			
			// Successfully parsed line header, now read points
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
				
				point := rm.Point{}
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
				
				// Validate values are not NaN or Inf, and are reasonable
				if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) ||
					math.IsNaN(float64(y)) || math.IsInf(float64(y), 0) ||
					math.IsNaN(float64(speed)) || math.IsInf(float64(speed), 0) ||
					math.IsNaN(float64(direction)) || math.IsInf(float64(direction), 0) ||
					math.IsNaN(float64(width)) || math.IsInf(float64(width), 0) ||
					math.IsNaN(float64(pressure)) || math.IsInf(float64(pressure), 0) {
					// Invalid point, skip it
					continue
				}
				
				// Also validate coordinates are reasonable (within page bounds)
				if x < -1000 || x > 20000 || y < -1000 || y > 20000 {
					// Coordinates out of reasonable bounds, skip
					continue
				}
				
				point.X = x
				point.Y = y
				point.Speed = speed
				point.Direction = direction
				point.Width = width
				point.Pressure = pressure
				
				line.Points[pointsRead] = point
				pointsRead++
			}
			
			// Resize points array to actual points read
			if pointsRead > 0 {
				line.Points = line.Points[:pointsRead]
			}
			
			if pointsRead > 0 {
				// Successfully parsed a line with at least some points
				lines = append(lines, line)
			} else {
				// Failed to read any valid points, try next position
				linePos = savedPos + 1
				continue
			}
		}
		
		rmData.Layers[layerIdx].Lines = lines
		
		// Move to next layer - skip past "Layer N" string if found
		if layerNamePos > 0 {
			// Find end of layer name string
			nameEnd := layerNamePos + 7
			for nameEnd < len(data) && data[nameEnd] != 0 && data[nameEnd] != '<' && nameEnd < layerNamePos+20 {
				nameEnd++
			}
			pos = nameEnd
		} else {
			// No layer name found, use linePos
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

	// If standard read failed or found no pages, try new format
	// Reset file position
	file.Seek(0, 0)
	return loadRmZipNewFormat(file)
}

func loadRmZipStandardOnly(filename string) (zipArchive *archive.Zip, err error) {
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
	
	log.Printf("Using standard rmapi parser only")
	err = zipArchive.Read(file, fi.Size())
	if err != nil {
		return nil, fmt.Errorf("standard parser failed: %w", err)
	}
	
	numPages := len(zipArchive.Pages)
	log.Printf("Standard parser found %d pages", numPages)
	
	// Verify pages have data
	for i, page := range zipArchive.Pages {
		if page.Data != nil {
			layers := len(page.Data.Layers)
			totalLines := 0
			totalPoints := 0
			for _, layer := range page.Data.Layers {
				totalLines += len(layer.Lines)
				for _, line := range layer.Lines {
					totalPoints += len(line.Points)
				}
			}
			log.Printf("Page %d: %d layers, %d lines, %d points", i, layers, totalLines, totalPoints)
		} else {
			log.Printf("Page %d: no data", i)
		}
	}
	
	return zipArchive, nil
}

func loadRmZipNewFormat(file *os.File) (zipArchive *archive.Zip, err error) {
	zipArchive = archive.NewZip()
	
	// Get file size for zip.NewReader
	fi, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("can't stat file: %w", err)
	}
	
	// Reset file position
	file.Seek(0, 0)
	
	// Open as standard ZIP archive
	reader, err := zip.NewReader(file, fi.Size())
	if err != nil {
		return nil, fmt.Errorf("can't open as zip: %w", err)
	}

	// Find the .content file to get page list
	var contentFile *zip.File
	var docUUID string
	for _, f := range reader.File {
		if strings.HasSuffix(f.Name, ".content") {
			contentFile = f
			// Extract UUID from filename (format: UUID.content)
			baseName := filepath.Base(f.Name)
			docUUID = strings.TrimSuffix(baseName, ".content")
			break
		}
	}

	if contentFile == nil {
		return nil, errors.New("no .content file found in archive")
	}

	// Read and parse content file
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
		// Pages are stored in subdirectories: UUID/pageID.rm
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

		// Read page data
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

		// Parse .rm file (now supports V3, V5, and V6)
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
		return nil, errors.New("no pages found in archive")
	}

	return zipArchive, nil
}

func main() {

	flag.Usage = func() {
		exec := os.Args[0]
		output := flag.CommandLine.Output()
		fmt.Fprintf(output, "Usage: %s [options] somefile.zip\n", exec)
		fmt.Fprintln(output, "\twhere somefile.zip is what you got with rmapi get")
		fmt.Fprintln(output, "\tOutputs: Text->text, Math->LaTex, Diagram->svg")
		fmt.Fprintln(output, "Options:")
		flag.PrintDefaults()
	}
	var inputType = flag.String("type", "Text", "type of the content: Text, Math, Diagram")
	var lang = flag.String("lang", "en_US", "language culture")
	//todo: page range, all pages etc
	var page = flag.Int("page", -1, "page to convert (default all)")
	//var outputFile = flag.String("o", "-", "output default stdout, wip")
	var addPages = flag.Bool("a", false, "add page headers")
	var visualize = flag.Bool("visualize", false, "render strokes to PNG for debugging (saves to <filename>_page_<N>.png)")
	var forceStandardParser = flag.Bool("force-standard", false, "force using standard rmapi parser (skip new format parser)")
	cfg := hwr.Config{
		Page:      *page,
		Lang:      *lang,
		InputType: *inputType,
		AddPages:  *addPages,
		BatchSize: *flag.Int64("b", 3, "batch size"),
	}
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		log.Fatal("no file specified")
	}

	filename := args[0]
	ext := path.Ext(filename)
	cfg.OutputFile = strings.TrimSuffix(filename, ext)

	var err error
	var z *archive.Zip

	switch ext {
	case ".zip", ".rmdoc":
		if *forceStandardParser {
			z, err = loadRmZipStandardOnly(filename)
		} else {
			z, err = loadRmZip(filename)
		}
	case ".rm":
		z, err = loadRmPage(filename)
	default:
		log.Fatal("Unsupported file")

	}
	if err != nil {
		log.Fatalln(err, "Can't read file ", filename)
	}

	// Visualize if requested
	if *visualize {
		pagesToVisualize := []int{}
		if *page >= 0 {
			pagesToVisualize = []int{*page - 1} // Convert to 0-based
		} else {
			// Visualize all pages
			for i := 0; i < len(z.Pages); i++ {
				pagesToVisualize = append(pagesToVisualize, i)
			}
		}

		for _, p := range pagesToVisualize {
			outputPNG := fmt.Sprintf("%s_page_%d.png", cfg.OutputFile, p)
			log.Printf("Visualizing page %d to %s", p, outputPNG)
			if err := hwr.VisualizePage(z, p, outputPNG); err != nil {
				log.Printf("Error visualizing page %d: %v", p, err)
			} else {
				log.Printf("Saved visualization to %s", outputPNG)
			}
		}
		return
	}

	hwr.Hwr(z, cfg)
}
