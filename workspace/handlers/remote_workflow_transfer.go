package handlers

// Archive transport for remote workflow moves. The removed workspace backup
// routes stay unavailable; these handlers are mounted only in server mode
// behind per-user authentication and workflow authorization.

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/utils"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// ExportRemoteWorkflowRequest represents the request to export a workspace
type ExportRemoteWorkflowRequest struct {
	WorkspacePath string `json:"workspace_path" binding:"required"`
}

// ImportRemoteWorkflowRequest represents the request to import a workspace backup
type ImportRemoteWorkflowRequest struct {
	WorkspacePath string `form:"workspace_path" binding:"required"`
	Overwrite     bool   `form:"overwrite"` // Whether to overwrite existing files
}

// ExportRemoteWorkflow supplies the archive used by a server-to-laptop workflow move.
// Creates a ZIP archive of the entire workspace folder
func ExportRemoteWorkflow(c *gin.Context) {
	var req ExportRemoteWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid request body",
			Error:   err.Error(),
		})
		return
	}

	docsDir := viper.GetString("docs-dir")
	userID := getUserID(c)

	// Resolve workspace path
	fullWorkspacePath, err := utils.ResolveUserPath(docsDir, req.WorkspacePath, userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid workspace path",
			Error:   err.Error(),
		})
		return
	}
	workspacePath, _ := utils.GetRelativePath(fullWorkspacePath, docsDir)

	// Check if workspace directory exists
	if info, err := os.Stat(fullWorkspacePath); err != nil || !info.IsDir() {
		c.JSON(http.StatusNotFound, models.APIResponse[any]{
			Success: false,
			Message: "Workspace not found",
			Error:   fmt.Sprintf("Workspace path does not exist: %s", workspacePath),
		})
		return
	}

	// Create a temporary ZIP file
	tempZipPath := filepath.Join(os.TempDir(), fmt.Sprintf("workspace-backup-%d.zip", time.Now().Unix()))
	defer os.Remove(tempZipPath) // Clean up temp file

	// Create ZIP file
	zipFile, err := os.Create(tempZipPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to create backup file",
			Error:   err.Error(),
		})
		return
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// Include the folder name as root directory in ZIP so import recreates the folder
	folderName := filepath.Base(fullWorkspacePath)

	// Walk through the workspace directory and add all files to ZIP
	err = filepath.Walk(fullWorkspacePath, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories (we'll add files only)
		if info.IsDir() {
			return nil
		}

		// Skip symlinks (they may point to directories or files outside the workspace)
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		// Calculate relative path from workspace root, prefixed with folder name
		relPath, err := filepath.Rel(fullWorkspacePath, filePath)
		if err != nil {
			return err
		}
		relPath = filepath.Join(folderName, relPath)

		// Open the file
		file, err := os.Open(filePath)
		if err != nil {
			return err
		}
		defer file.Close()

		// Create a file in the ZIP
		zipEntry, err := zipWriter.Create(relPath)
		if err != nil {
			return err
		}

		// Copy file contents to ZIP
		_, err = io.Copy(zipEntry, file)
		return err
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to create backup archive",
			Error:   err.Error(),
		})
		return
	}

	// Close ZIP writer to finalize the archive
	if err := zipWriter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to finalize backup archive",
			Error:   err.Error(),
		})
		return
	}

	// Generate filename for download
	workspaceName := filepath.Base(workspacePath)
	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s-backup-%s.zip", workspaceName, timestamp)

	// Set headers for file download
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	c.Header("Content-Transfer-Encoding", "binary")

	// Open the ZIP file for reading
	zipFileReader, err := os.Open(tempZipPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to read backup file",
			Error:   err.Error(),
		})
		return
	}
	defer zipFileReader.Close()

	// Get file info for Content-Length
	zipFileInfo, err := zipFileReader.Stat()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to get backup file info",
			Error:   err.Error(),
		})
		return
	}

	c.Header("Content-Length", fmt.Sprintf("%d", zipFileInfo.Size()))

	// Stream the ZIP file to the client
	c.DataFromReader(http.StatusOK, zipFileInfo.Size(), "application/zip", zipFileReader, map[string]string{
		"Content-Disposition": fmt.Sprintf("attachment; filename=\"%s\"", filename),
	})
}

// ImportRemoteWorkflow receives a laptop-to-server workflow move.
// Extracts a ZIP archive to restore a workspace folder
func ImportRemoteWorkflow(c *gin.Context) {
	fmt.Println("📥 Received ImportRemoteWorkflow request")

	// Parse form data
	var req ImportRemoteWorkflowRequest
	if err := c.ShouldBind(&req); err != nil {
		fmt.Printf("❌ Failed to bind request: %v\n", err)
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid request",
			Error:   err.Error(),
		})
		return
	}
	fmt.Printf("📝 Request bound. Path: %s, Overwrite: %v\n", req.WorkspacePath, req.Overwrite)

	// Get the uploaded file
	file, err := c.FormFile("file")
	if err != nil {
		fmt.Printf("❌ Failed to get form file: %v\n", err)
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "No file uploaded",
			Error:   err.Error(),
		})
		return
	}
	fmt.Printf("📂 Received file: %s, Size: %d bytes\n", file.Filename, file.Size)

	// Validate file extension
	if !strings.HasSuffix(strings.ToLower(file.Filename), ".zip") {
		fmt.Println("❌ Invalid file extension")
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid file type",
			Error:   "Only ZIP files are supported",
		})
		return
	}

	docsDir := viper.GetString("docs-dir")
	userID := getUserID(c)

	// Resolve workspace path
	fullWorkspacePath, resolveErr := utils.ResolveUserPath(docsDir, req.WorkspacePath, userID)
	if resolveErr != nil {
		fmt.Printf("❌ Failed to resolve path: %v\n", resolveErr)
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid workspace path",
			Error:   resolveErr.Error(),
		})
		return
	}
	workspacePath, _ := utils.GetRelativePath(fullWorkspacePath, docsDir)
	fmt.Printf("📍 Target path: %s\n", fullWorkspacePath)

	// Check if workspace directory exists (if not overwriting, we might want to create it)
	if !req.Overwrite {
		if _, err := os.Stat(fullWorkspacePath); err == nil {
			fmt.Println("❌ Workspace exists and overwrite is false")
			c.JSON(http.StatusConflict, models.APIResponse[any]{
				Success: false,
				Message: "Workspace already exists",
				Error:   fmt.Sprintf("Workspace path already exists: %s. Use overwrite=true to replace it.", workspacePath),
			})
			return
		}
	}

	// Create workspace directory if it doesn't exist
	if err := os.MkdirAll(fullWorkspacePath, 0755); err != nil {
		fmt.Printf("❌ Failed to create directory: %v\n", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to create workspace directory",
			Error:   err.Error(),
		})
		return
	}

	// Save uploaded file to temporary location
	// Save into a private temp dir: gin's SaveUploadedFile chmods the
	// destination's parent, which fails on the shared system temp dir (macOS).
	tempDir, err := os.MkdirTemp("", "workspace-import-*")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to create temp directory",
			Error:   err.Error(),
		})
		return
	}
	defer os.RemoveAll(tempDir)
	tempZipPath := filepath.Join(tempDir, fmt.Sprintf("workspace-import-%d.zip", time.Now().Unix()))
	fmt.Printf("💾 Saving to temp file: %s\n", tempZipPath)
	if err := c.SaveUploadedFile(file, tempZipPath); err != nil {
		fmt.Printf("❌ Failed to save file: %v\n", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
			Success: false,
			Message: "Failed to save uploaded file",
			Error:   err.Error(),
		})
		return
	}
	defer os.Remove(tempZipPath) // Clean up temp file
	fmt.Println("✅ File saved successfully")

	// Open the ZIP file
	zipReader, err := zip.OpenReader(tempZipPath)
	if err != nil {
		fmt.Printf("❌ Failed to open ZIP: %v\n", err)
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{
			Success: false,
			Message: "Invalid ZIP file",
			Error:   err.Error(),
		})
		return
	}
	defer zipReader.Close()
	fmt.Printf("📦 ZIP opened. Contains %d files\n", len(zipReader.File))

	// Extract all files from the ZIP
	var extractedFiles []string
	for _, zipFile := range zipReader.File {
		// Sanitize the file name to remove characters invalid on Azure Files/Windows
		// This prevents "invalid argument" errors when extracting to mounted Azure File Shares
		cleanName := zipFile.Name
		cleanName = strings.ReplaceAll(cleanName, "\\", "_")
		cleanName = strings.ReplaceAll(cleanName, ":", "_")
		cleanName = strings.ReplaceAll(cleanName, "*", "_")
		cleanName = strings.ReplaceAll(cleanName, "?", "_")
		cleanName = strings.ReplaceAll(cleanName, "\"", "_")
		cleanName = strings.ReplaceAll(cleanName, "<", "_")
		cleanName = strings.ReplaceAll(cleanName, ">", "_")
		cleanName = strings.ReplaceAll(cleanName, "|", "_")

		// Sanitize the file path to prevent directory traversal
		filePath := filepath.Join(fullWorkspacePath, cleanName)

		// Ensure the extracted path is within the workspace directory
		if !strings.HasPrefix(filePath, fullWorkspacePath) {
			fmt.Printf("⚠️  Skipping invalid path: %s\n", zipFile.Name)
			c.JSON(http.StatusBadRequest, models.APIResponse[any]{
				Success: false,
				Message: "Invalid file path in archive",
				Error:   fmt.Sprintf("Path traversal detected: %s", zipFile.Name),
			})
			return
		}

		// Create directory structure if needed
		if zipFile.FileInfo().IsDir() {
			if err := os.MkdirAll(filePath, 0755); err != nil {
				fmt.Printf("❌ Failed to create subdirectory %s: %v\n", filePath, err)
				c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
					Success: false,
					Message: "Failed to create directory",
					Error:   err.Error(),
				})
				return
			}
			continue
		}

		// Create parent directories
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			fmt.Printf("❌ Failed to create parent directory for %s: %v\n", filePath, err)
			c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
				Success: false,
				Message: "Failed to create directory structure",
				Error:   err.Error(),
			})
			return
		}

		// Open file from ZIP
		zipFileReader, err := zipFile.Open()
		if err != nil {
			fmt.Printf("❌ Failed to read file %s from zip: %v\n", zipFile.Name, err)
			c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
				Success: false,
				Message: "Failed to read file from archive",
				Error:   err.Error(),
			})
			return
		}

		// Create destination file
		destFile, err := os.Create(filePath)
		if err != nil {
			zipFileReader.Close()
			fmt.Printf("❌ Failed to create destination file %s: %v\n", filePath, err)
			c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
				Success: false,
				Message: "Failed to create destination file",
				Error:   err.Error(),
			})
			return
		}

		// Copy file contents
		_, err = io.Copy(destFile, zipFileReader)
		destFile.Close()
		zipFileReader.Close()

		if err != nil {
			fmt.Printf("❌ Failed to extract file content %s: %v\n", filePath, err)
			c.JSON(http.StatusInternalServerError, models.APIResponse[any]{
				Success: false,
				Message: "Failed to extract file",
				Error:   err.Error(),
			})
			return
		}

		extractedFiles = append(extractedFiles, zipFile.Name)
	}

	fmt.Printf("✅ Import completed. Extracted %d files.\n", len(extractedFiles))
	c.JSON(http.StatusOK, models.APIResponse[map[string]interface{}]{
		Success: true,
		Message: "Workspace backup imported successfully",
		Data: map[string]interface{}{
			"workspace_path":  workspacePath,
			"files_extracted": len(extractedFiles),
			"extracted_files": extractedFiles,
		},
	})
}
