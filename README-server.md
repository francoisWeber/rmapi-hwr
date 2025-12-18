# rmapi-hwr REST API Server Documentation

This document describes all available endpoints in the rmapi-hwr HTTP server for processing reMarkable2 `.rmdoc` files.

## Base URL

The server runs on port `8082` by default (configurable via `PORT` environment variable).

## Overview

The rmapi-hwr server provides two main functionalities:
1. **Handwriting Recognition (HWR)**: Convert handwritten strokes from `.rmdoc` files to text/markdown
2. **PNG Conversion**: Convert `.rmdoc` files to PNG images

## Configuration

The server requires the following environment variables:

- `PORT` (optional): Server port (default: `8082`)
- `OUTPUT_DIR` (optional): Directory for temporary files (default: `/tmp/rmapi-hwr-output`)
- `RMAPI_HWR_APPLICATIONKEY` (required for HWR): MyScript application key
- `RMAPI_HWR_HMAC` (required for HWR): MyScript HMAC key

## Endpoints

### Health Check

#### `GET /health`
Check if the server is running.

**Response:**
```json
{
  "status": "ok",
  "time": "2024-12-09T10:30:00Z"
}
```

**Example:**
```bash
curl http://localhost:8082/health
```

**Response:**
```json
{
  "status": "ok",
  "time": "2024-12-09T10:30:00Z"
}
```

---

### Handwriting Recognition

#### `POST /api/hwr`
Convert handwritten content from a `.rmdoc` file to text using MyScript handwriting recognition.

**Request:**
- Method: `POST`
- Content-Type: `multipart/form-data`
- Body: Form data with a file field named `file`

**Form Parameters:**
- `file` (file, required): The `.rmdoc` or `.zip` file to process
- `type` (string, optional): Content type - `Text`, `Math`, or `Diagram` (default: `Text`)
- `lang` (string, optional): Language code (default: `en_US`)
  - Examples: `en_US`, `fr_FR`, `de_DE`, `es_ES`, `it_IT`, `pt_PT`, `ja_JP`, `zh_CN`, etc.
- `page` (integer, optional): Specific page number to process (1-indexed)
  - If omitted or negative, processes all pages
  - If `0`, processes the last opened page

**Response:**
```json
{
  "filename": "my-notes.rmdoc",
  "pages": 3,
  "text": {
    "0": "This is the text from page 1",
    "1": "This is the text from page 2",
    "2": "This is the text from page 3"
  }
}
```

**Example 1: Convert all pages to text (default language)**
```bash
curl -X POST http://localhost:8082/api/hwr \
  -F "file=@my-notes.rmdoc"
```

**Example 2: Convert specific page to text**
```bash
curl -X POST http://localhost:8082/api/hwr \
  -F "file=@my-notes.rmdoc" \
  -F "page=2"
```

**Example 3: Convert with French language**
```bash
curl -X POST http://localhost:8082/api/hwr \
  -F "file=@my-notes.rmdoc" \
  -F "lang=fr_FR"
```

**Example 4: Convert math content**
```bash
curl -X POST http://localhost:8082/api/hwr \
  -F "file=@math-equations.rmdoc" \
  -F "type=Math"
```

**Example 5: Convert diagram to SVG**
```bash
curl -X POST http://localhost:8082/api/hwr \
  -F "file=@diagram.rmdoc" \
  -F "type=Diagram"
```

**Example 6: Using Python requests**
```python
import requests

url = "http://localhost:8082/api/hwr"
files = {"file": open("my-notes.rmdoc", "rb")}
data = {
    "type": "Text",
    "lang": "en_US",
    "page": -1  # All pages
}

response = requests.post(url, files=files, data=data)
result = response.json()

for page_num, text in result["text"].items():
    print(f"Page {page_num}: {text}")
```

**Example 7: Using JavaScript/Node.js**
```javascript
const FormData = require('form-data');
const fs = require('fs');
const axios = require('axios');

const form = new FormData();
form.append('file', fs.createReadStream('my-notes.rmdoc'));
form.append('type', 'Text');
form.append('lang', 'en_US');

axios.post('http://localhost:8082/api/hwr', form, {
  headers: form.getHeaders()
})
.then(response => {
  console.log('Recognition result:', response.data);
  Object.entries(response.data.text).forEach(([page, text]) => {
    console.log(`Page ${page}: ${text}`);
  });
})
.catch(error => {
  console.error('Error:', error.response?.data || error.message);
});
```

**Error Responses:**

- `400 Bad Request`: Invalid file format or missing file
  ```json
  {
    "error": "Error loading rmdoc: ..."
  }
  ```

- `404 Not Found`: No content found in the document
  ```json
  {
    "error": "No content found"
  }
  ```

- `500 Internal Server Error`: HWR credentials not configured or processing error
  ```json
  {
    "error": "HWR credentials not configured"
  }
  ```

---

### PNG Conversion

#### `POST /api/convert`
Convert a `.rmdoc` file to PNG images (one PNG per page).

**Request:**
- Method: `POST`
- Content-Type: `multipart/form-data`
- Body: Form data with a file field named `file`

**Form Parameters:**
- `file` (file, required): The `.rmdoc` or `.zip` file to convert
- `page` (integer, optional): Specific page number to convert (1-indexed)
  - If omitted or negative, converts all pages
  - If `0`, converts the last opened page

**Response:**
- Content-Type: `application/zip`
- Content-Disposition: `attachment; filename=<original_filename>_pages.zip`
- Body: ZIP file containing PNG images named `page_0.png`, `page_1.png`, etc.

**Example 1: Convert all pages to PNG**
```bash
curl -X POST http://localhost:8082/api/convert \
  -F "file=@my-notes.rmdoc" \
  -o output.zip
```

**Example 2: Convert specific page to PNG**
```bash
curl -X POST http://localhost:8082/api/convert \
  -F "file=@my-notes.rmdoc" \
  -F "page=1" \
  -o page1.png.zip
```

**Example 3: Extract PNGs from ZIP**
```bash
# Download the ZIP
curl -X POST http://localhost:8082/api/convert \
  -F "file=@my-notes.rmdoc" \
  -o pages.zip

# Extract PNGs
unzip pages.zip
# Results in: page_0.png, page_1.png, etc.
```

**Example 4: Using Python requests**
```python
import requests
import zipfile
import io

url = "http://localhost:8082/api/convert"
files = {"file": open("my-notes.rmdoc", "rb")}
data = {"page": -1}  # All pages

response = requests.post(url, files=files, data=data)

# Extract PNGs from ZIP
with zipfile.ZipFile(io.BytesIO(response.content)) as zip_file:
    zip_file.extractall("output_pngs")
    print(f"Extracted {len(zip_file.namelist())} PNG files")
```

**Example 5: Using JavaScript/Node.js**
```javascript
const FormData = require('form-data');
const fs = require('fs');
const axios = require('axios');
const AdmZip = require('adm-zip');

const form = new FormData();
form.append('file', fs.createReadStream('my-notes.rmdoc'));
form.append('page', '-1'); // All pages

axios.post('http://localhost:8082/api/convert', form, {
  headers: form.getHeaders(),
  responseType: 'arraybuffer'
})
.then(response => {
  const zip = new AdmZip(response.data);
  zip.extractAllTo('./output_pngs', true);
  console.log(`Extracted ${zip.getEntries().length} PNG files`);
})
.catch(error => {
  console.error('Error:', error.response?.data || error.message);
});
```

**Error Responses:**

- `400 Bad Request`: Invalid file format or missing file
  ```json
  {
    "error": "Error loading rmdoc: ..."
  }
  ```

- `500 Internal Server Error`: Conversion error
  ```json
  {
    "error": "No pages converted"
  }
  ```

---

## Content Types

### Text Recognition (`type=Text`)
Converts handwritten text to plain text. Supports various languages.

**Supported Languages:**
- `en_US` - English (US)
- `en_GB` - English (UK)
- `fr_FR` - French
- `de_DE` - German
- `es_ES` - Spanish
- `it_IT` - Italian
- `pt_PT` - Portuguese
- `ja_JP` - Japanese
- `zh_CN` - Chinese (Simplified)
- `zh_TW` - Chinese (Traditional)
- `ko_KR` - Korean
- And more...

### Math Recognition (`type=Math`)
Converts handwritten mathematical equations to LaTeX format.

**Example Response:**
```json
{
  "filename": "equations.rmdoc",
  "pages": 1,
  "text": {
    "0": "x^2 + y^2 = r^2"
  }
}
```

### Diagram Recognition (`type=Diagram`)
Converts handwritten diagrams to SVG format.

**Example Response:**
```json
{
  "filename": "diagram.rmdoc",
  "pages": 1,
  "text": {
    "0": "<svg>...</svg>"
  }
}
```

---

## File Format

The server accepts `.rmdoc` files, which are ZIP archives containing:
- `.content` file: Metadata about the document and pages
- `UUID/pageID.rm` files: Binary stroke data for each page
- Other metadata files

The server automatically handles both standard and newer reMarkable2 file formats.

---

## Docker Usage

### Building and Running

```bash
# Build the image
docker-compose build hwr

# Run the server
docker-compose up hwr

# Or run in detached mode
docker-compose up -d hwr
```

### Environment Variables

Set these in your `docker-compose.yml` or `.env` file:

```yaml
environment:
  - PORT=8082
  - OUTPUT_DIR=/tmp/rmapi-hwr-output
  - RMAPI_HWR_APPLICATIONKEY=your_application_key_here
  - RMAPI_HWR_HMAC=your_hmac_key_here
```

### Volumes

The server uses a volume for temporary output files:

```yaml
volumes:
  - ./io/hwr/output:/tmp/rmapi-hwr-output
```

---

## Rate Limiting

Currently, there is no rate limiting implemented. However, the HWR service uses a batch size of 3 pages processed concurrently to balance performance and API limits.

---

## Troubleshooting

### HWR Credentials Not Configured
If you see `"HWR credentials not configured"`, make sure:
1. `RMAPI_HWR_APPLICATIONKEY` is set
2. `RMAPI_HWR_HMAC` is set
3. Both environment variables are available to the container

### No Content Found
If you see `"No content found"`:
1. Verify the `.rmdoc` file is valid
2. Check that the file contains pages with stroke data
3. Ensure the file format is supported

### Conversion Errors
If PNG conversion fails:
1. Check server logs for detailed error messages
2. Verify the `.rmdoc` file is not corrupted
3. Ensure sufficient disk space in the output directory

---

## API Response Format

All successful responses follow a consistent format:

**HWR Response:**
```json
{
  "filename": "string",
  "pages": number,
  "text": {
    "page_index": "recognized_text"
  }
}
```

**Error Response:**
```json
{
  "error": "error_message"
}
```

---

## License

See the LICENSE file in the repository root.

