# Extract Strokes from .rm Files

This tool extracts strokes from Remarkable `.rm` files and outputs them as JSON.

## Usage

```bash
# Build the tool
go build -o extract-strokes ./cmd/extract-strokes

# Extract strokes from a .rm file (output to stdout)
./extract-strokes file.rm

# Save to a file
./extract-strokes -o output.json file.rm

# Pretty print JSON
./extract-strokes -pretty file.rm

# Pretty print and save to file
./extract-strokes -pretty -o output.json file.rm
```

## Example Output

The tool outputs JSON with the following structure:

```json
{
  "version": 5,
  "layers": [
    {
      "strokes": [
        {
          "x": [100.5, 105.2, 110.8, ...],
          "y": [200.3, 205.1, 210.5, ...],
          "pressure": [0.5, 0.6, 0.7, ...],
          "speed": [10.2, 12.5, 15.0, ...],
          "width": [2.0, 2.1, 2.2, ...],
          "direction": [0.0, 0.1, 0.2, ...],
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

## What it does

- Reads `.rm` files using the `rmapi` library
- Extracts all strokes (lines) from all layers
- Converts each stroke to JSON format with:
  - X/Y coordinates
  - Pressure, speed, width, direction for each point
  - Brush type, color, and size
  - Pointer type (PEN or ERASER)
- Skips erase area strokes
- Outputs summary statistics to stderr

## Comparison with other tools

- **`rmhwr`**: Sends strokes to MyScript API for handwriting recognition (requires API keys)
- **`tojson`**: Converts strokes to MyScript API format, but only works with `.zip` files
- **`extract-strokes`**: Extracts raw strokes from `.rm` files as JSON (no API required)

