package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// Inline images bypass text truncation. Also materialize them under the
// parent-selected session output directory so native image/file tools can read
// them when a CLI does not render MCP image blocks in its model context.
func bridgeResultWithImages(text string, images []mcp.ImageContent, outputDir string) *mcp.CallToolResult {
	result := mcp.NewToolResultText(text)
	// Keep the complete stdio response below the transport's historical 1 MiB
	// boundary. Large images remain available as exact files, never truncated.
	const inlineBudget = 512 * 1024
	inlineBytes := 0
	for i, image := range images {
		data, extension, err := decodeBridgeImage(image)
		if err != nil {
			result.Content = append(result.Content, mcp.TextContent{Type: "text", Text: fmt.Sprintf("Image %d could not be delivered: %v", i+1, err)})
			continue
		}
		image.Type = "image"
		inline := len(image.Data) <= inlineBudget-inlineBytes
		if inline {
			result.Content = append(result.Content, image)
			inlineBytes += len(image.Data)
		}
		path, err := persistBridgeImage(outputDir, data, extension)
		if err != nil {
			availability := "could not be delivered inline"
			if inline {
				availability = "is attached as an MCP image block"
			}
			result.Content = append(result.Content, mcp.TextContent{Type: "text", Text: fmt.Sprintf("Image %d %s; saving a local file failed: %v", i+1, availability, err)})
			continue
		}
		result.Content = append(result.Content, mcp.TextContent{Type: "text", Text: fmt.Sprintf("Image %d saved to: %s\nOpen this exact file with your native image-capable file tool, or use the session's read_image tool. The preceding text is not a visual analysis of this image.", i+1, path)})
	}
	return result
}

func decodeBridgeImage(image mcp.ImageContent) ([]byte, string, error) {
	// Bound decoding independently of text offloading. Never build file paths
	// from the external tool's MIME type or arguments.
	const maxImageBytes = 20 * 1024 * 1024
	if len(image.Data) > base64.StdEncoding.EncodedLen(maxImageBytes) {
		return nil, "", fmt.Errorf("image exceeds the 20 MiB local-file limit")
	}
	data, err := base64.StdEncoding.DecodeString(image.Data)
	if err != nil {
		return nil, "", fmt.Errorf("invalid image base64")
	}
	if len(data) > maxImageBytes {
		return nil, "", fmt.Errorf("image exceeds the 20 MiB local-file limit")
	}
	extensions := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}
	extension, ok := extensions[image.MIMEType]
	if !ok || http.DetectContentType(data) != image.MIMEType {
		return nil, "", fmt.Errorf("unsupported image format or MIME mismatch")
	}
	return data, extension, nil
}

func persistBridgeImage(outputDir string, data []byte, extension string) (string, error) {
	if strings.TrimSpace(outputDir) == "" {
		return "", fmt.Errorf("session tool output directory is unavailable")
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return "", fmt.Errorf("create session tool output directory: %w", err)
	}
	file, err := os.CreateTemp(outputDir, "mcp-image-*"+extension)
	if err != nil {
		return "", fmt.Errorf("create image file: %w", err)
	}
	path := file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write image file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close image file: %w", err)
	}
	return path, nil
}
