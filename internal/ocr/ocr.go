// Package ocr extracts text from page images by shelling out to tesseract.
package ocr

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Available reports whether tesseract is on PATH.
func Available() bool {
	_, err := exec.LookPath("tesseract")
	return err == nil
}

// Lang returns the tesseract language, from CBZR_OCR_LANG or "eng".
func Lang() string {
	if l := os.Getenv("CBZR_OCR_LANG"); l != "" {
		return l
	}
	return "eng"
}

// Text runs tesseract on img and returns the recognized text.
func Text(img image.Image) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var in bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&in, img); err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "tesseract", "stdin", "stdout", "-l", Lang())
	cmd.Stdin = &in
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		if len(msg) > 120 {
			msg = msg[:120]
		}
		return "", fmt.Errorf("tesseract: %s", msg)
	}
	return out.String(), nil
}
