package client

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"net/http"
)

const url = "https://cloud.myscript.com/api/v4.0/iink/batch"

func SendRequest(key, hmackey string, data []byte, mimeType string) (body []byte, err error) {
	fullkey := key + hmackey
	mac := hmac.New(sha512.New, []byte(fullkey))
	mac.Write(data)
	result := hex.EncodeToString(mac.Sum(nil))

	client := http.Client{}

	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	req.Header.Set("Accept", mimeType+", application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("applicationKey", key)
	req.Header.Set("hmac", result)

	res, err := client.Do(req)

	if err != nil {
		return
	}
	defer res.Body.Close()
	
	body, err = ioutil.ReadAll(res.Body)
	if err != nil {
		return
	}

	// Log response headers for debugging
	if res.StatusCode != http.StatusOK {
		err = fmt.Errorf("Not ok, Status: %d, Response: %s", res.StatusCode, string(body))
		return
	}
	
	// Log content type to see what format we actually got
	contentType := res.Header.Get("Content-Type")
	if contentType != "" {
		fmt.Printf("Response Content-Type: %s\n", contentType)
	}

	return body, nil
}
