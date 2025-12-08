# Examples: Extracting Strokes from .rm Files

This document shows different ways to extract strokes from Remarkable `.rm` files.

## Solution 1: Using `extract-strokes` tool (Recommended)

The simplest way to extract strokes as JSON without requiring API keys:

```bash
# Build the tool
cd rmapi-hwr
go build -o extract-strokes ./cmd/extract-strokes

# Extract strokes to JSON
./extract-strokes -pretty -o strokes.json file.rm

# View output
cat strokes.json
```

**Output format:**
```json
{
  "version": 5,
  "layers": [
    {
      "strokes": [
        {
          "x": [100.5, 105.2, ...],
          "y": [200.3, 205.1, ...],
          "pressure": [0.5, 0.6, ...],
          "brushType": 15,
          "brushColor": 0,
          "brushSize": 2.0,
          "pointerType": "PEN"
        }
      ]
    }
  ]
}
```

## Solution 2: Using `rmhwr` with visualization

Visualize strokes as PNG images (no API required):

```bash
# Build rmhwr
cd rmapi-hwr
go build -o rmhwr ./cmd/rmhwr

# Visualize strokes to PNG
./rmhwr -visualize file.rm

# This creates: file_page_0.png, file_page_1.png, etc.
```

## Solution 3: Programmatic usage with Go

Here's how to use the `rmapi` library directly in your own Go code:

```go
package main

import (
    "encoding/json"
    "fmt"
    "io"
    "os"
    
    "github.com/juruen/rmapi/encoding/rm"
)

func main() {
    // Open .rm file
    file, err := os.Open("file.rm")
    if err != nil {
        panic(err)
    }
    defer file.Close()
    
    // Read file data
    data, err := io.ReadAll(file)
    if err != nil {
        panic(err)
    }
    
    // Parse .rm file
    rmData := rm.New()
    err = rmData.UnmarshalBinary(data)
    if err != nil {
        panic(err)
    }
    
    // Extract strokes
    for layerIdx, layer := range rmData.Layers {
        fmt.Printf("Layer %d: %d strokes\n", layerIdx, len(layer.Lines))
        
        for lineIdx, line := range layer.Lines {
            // Skip erase areas
            if line.BrushType == rm.EraseArea {
                continue
            }
            
            fmt.Printf("  Stroke %d: %d points\n", lineIdx, len(line.Points))
            
            // Access point data
            for _, point := range line.Points {
                // point.X, point.Y, point.Pressure, etc.
                fmt.Printf("    Point: (%.2f, %.2f) pressure=%.2f\n", 
                    point.X, point.Y, point.Pressure)
            }
        }
    }
}
```

## Solution 4: Using `tojson` for MyScript API format

If you need strokes in MyScript API format (for handwriting recognition):

```bash
# Note: tojson only works with .zip files, not standalone .rm files
cd rmapi-hwr
go build -o tojson ./cmd/tojson

# Convert .zip file to MyScript API JSON format
./tojson -pretty -o api_format.json notebook.zip
```

## Comparison

| Tool | Input Format | Output Format | Requires API? | Use Case |
|------|-------------|---------------|---------------|----------|
| `extract-strokes` | `.rm` | JSON (raw strokes) | No | Extract raw stroke data |
| `rmhwr -visualize` | `.rm`, `.zip`, `.rmdoc` | PNG images | No | Visualize strokes |
| `rmhwr` | `.rm`, `.zip`, `.rmdoc` | Text/Math/Diagram | Yes | Handwriting recognition |
| `tojson` | `.zip` | JSON (MyScript API) | No | Prepare for API |

## Quick Test

Test with a sample file:

```bash
# Extract strokes from a test file
cd rmapi-hwr
./extract-strokes -pretty ../rmapi/encoding/rm/test_v5.rm

# Visualize it
./rmhwr -visualize ../rmapi/encoding/rm/test_v5.rm
```

