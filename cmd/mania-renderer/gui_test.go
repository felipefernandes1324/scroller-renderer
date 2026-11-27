package main

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseMultipartFormHandlesSkinsOverThousandFiles(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	for i := 0; i < 1101; i++ {
		name := fmt.Sprintf("Skin/asset-%04d.png", i)
		file, err := writer.CreateFormFile("skinFiles", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte{0}); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("skinRelativePaths", name); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/render", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if err := request.ParseMultipartForm(16 << 20); err != nil {
		t.Fatalf("ParseMultipartForm rejected a skin with 1101 files: %v", err)
	}
	if got := len(request.MultipartForm.File["skinFiles"]); got != 1101 {
		t.Fatalf("got %d skin files, want 1101", got)
	}
	if got := len(request.MultipartForm.Value["skinRelativePaths"]); got != 1101 {
		t.Fatalf("got %d skin paths, want 1101", got)
	}
}
