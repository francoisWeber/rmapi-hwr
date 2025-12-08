package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/juruen/rmapi/encoding/rm"
)

// Stroke represents a simplified stroke structure for JSON output
type Stroke struct {
	X           []float32 `json:"x"`
	Y           []float32 `json:"y"`
	Pressure    []float32 `json:"pressure,omitempty"`
	Speed       []float32 `json:"speed,omitempty"`
	Width       []float32 `json:"width,omitempty"`
	Direction   []float32 `json:"direction,omitempty"`
	BrushType   uint32    `json:"brushType"`
	BrushColor  uint32    `json:"brushColor"`
	BrushSize   float32   `json:"brushSize"`
	PointerType string    `json:"pointerType,omitempty"`
}

// PageData represents the extracted strokes from a page
type PageData struct {
	Version int      `json:"version"`
	Layers  []Layer  `json:"layers"`
}

// Layer represents a layer with strokes
type Layer struct {
	Strokes []Stroke `json:"strokes"`
}

func main() {
	flag.Usage = func() {
		exec := os.Args[0]
		output := flag.CommandLine.Output()
		fmt.Fprintf(output, "Usage: %s [options] <file.rm>\n", exec)
		fmt.Fprintln(output, "\tExtracts strokes from a Remarkable .rm file and outputs as JSON")
		fmt.Fprintln(output, "Options:")
		flag.PrintDefaults()
	}

	var outputFile = flag.String("o", "", "output file (default stdout)")
	var pretty = flag.Bool("pretty", false, "pretty print JSON")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		log.Fatal("no .rm file specified")
	}

	filename := args[0]

	// Read the .rm file
	file, err := os.Open(filename)
	if err != nil {
		log.Fatalf("can't open file: %v", err)
	}
	defer file.Close()

	pageData, err := io.ReadAll(file)
	if err != nil {
		log.Fatalf("can't read file: %v", err)
	}

	// Parse the .rm file
	rmData := rm.New()
	err = rmData.UnmarshalBinary(pageData)
	if err != nil {
		log.Fatalf("can't parse .rm file: %v", err)
	}

	// Convert to our JSON structure
	page := PageData{
		Version: int(rmData.Version),
		Layers:  make([]Layer, len(rmData.Layers)),
	}

	for layerIdx, rmLayer := range rmData.Layers {
		layer := Layer{
			Strokes: make([]Stroke, 0),
		}

		for _, line := range rmLayer.Lines {
			// Skip erase area strokes
			if line.BrushType == rm.EraseArea {
				continue
			}

			// Skip empty lines
			if len(line.Points) == 0 {
				continue
			}

			// Determine pointer type
			pointerType := "PEN"
			if line.BrushType == rm.Eraser {
				pointerType = "ERASER"
			}

			// Extract stroke data
			stroke := Stroke{
				X:           make([]float32, len(line.Points)),
				Y:           make([]float32, len(line.Points)),
				Pressure:    make([]float32, len(line.Points)),
				Speed:       make([]float32, len(line.Points)),
				Width:       make([]float32, len(line.Points)),
				Direction:   make([]float32, len(line.Points)),
				BrushType:   uint32(line.BrushType),
				BrushColor:  uint32(line.BrushColor),
				BrushSize:   float32(line.BrushSize),
				PointerType: pointerType,
			}

			for i, point := range line.Points {
				stroke.X[i] = point.X
				stroke.Y[i] = point.Y
				stroke.Pressure[i] = point.Pressure
				stroke.Speed[i] = point.Speed
				stroke.Width[i] = point.Width
				stroke.Direction[i] = point.Direction
			}

			layer.Strokes = append(layer.Strokes, stroke)
		}

		page.Layers[layerIdx] = layer
	}

	// Output JSON
	var jsonData []byte
	if *pretty {
		jsonData, err = json.MarshalIndent(page, "", "  ")
	} else {
		jsonData, err = json.Marshal(page)
	}
	if err != nil {
		log.Fatalf("can't marshal JSON: %v", err)
	}

	// Write output
	if *outputFile == "" {
		fmt.Println(string(jsonData))
	} else {
		err = os.WriteFile(*outputFile, jsonData, 0644)
		if err != nil {
			log.Fatalf("can't write output file: %v", err)
		}
		fmt.Printf("Strokes extracted to %s\n", *outputFile)
	}

	// Print summary
	totalStrokes := 0
	totalPoints := 0
	for _, layer := range page.Layers {
		totalStrokes += len(layer.Strokes)
		for _, stroke := range layer.Strokes {
			totalPoints += len(stroke.X)
		}
	}
	fmt.Fprintf(os.Stderr, "Extracted %d layers, %d strokes, %d points\n", 
		len(page.Layers), totalStrokes, totalPoints)
}

