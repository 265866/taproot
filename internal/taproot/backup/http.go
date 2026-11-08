package backup

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// Routes registers backup endpoints under the taproot API group.
func (b *Backups) Routes(r *gin.RouterGroup) {
	r.GET("/backups", b.handleList)
	r.POST("/backups", b.handleCreate)
	r.GET("/backups/:id/download", b.handleDownload)
	r.POST("/backups/:id/restore", b.handleRestore)
}

func (b *Backups) handleList(c *gin.Context) {
	entries, err := b.List()
	if err != nil {
		writeError(c, err)
		return
	}
	if entries == nil {
		entries = []Entry{}
	}
	c.JSON(http.StatusOK, gin.H{"backups": entries})
}

func (b *Backups) handleCreate(c *gin.Context) {
	entry, err := b.Create(c.Request.Context(), ReasonManual)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, entry)
}

func (b *Backups) handleDownload(c *gin.Context) {
	id := c.Param("id")
	path, err := b.archivePath(id)
	if err != nil {
		writeError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Type", "application/zstd")
	c.FileAttachment(path, id+archiveExt)
}

type restoreResponse struct {
	Restored           bool   `json:"restored"`
	PreRestoreBackupID string `json:"preRestoreBackupId"`
	// Restart is "scheduled" when the process will exit for its supervisor to
	// restart it, or "manual" when an operator must restart it.
	Restart string `json:"restart"`
	Message string `json:"message"`
}

func (b *Backups) handleRestore(c *gin.Context) {
	result, err := b.Restore(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeError(c, err)
		return
	}
	resp := restoreResponse{Restored: true, PreRestoreBackupID: result.PreRestoreBackupID}
	if b.restart == nil {
		resp.Restart = "manual"
		resp.Message = "Config and auth files are restored. Restart taproot to finish restoring the database."
		c.JSON(http.StatusOK, resp)
		return
	}
	resp.Restart = "scheduled"
	resp.Message = "Config and auth files are restored. Taproot is restarting to finish restoring the database."
	c.JSON(http.StatusOK, resp)
	c.Writer.Flush()
	go func() {
		if errRestart := b.restart(); errRestart != nil {
			log.WithError(errRestart).Error("taproot backups: restart after restore failed; restart manually")
		}
	}()
}

func writeError(c *gin.Context, err error) {
	status, code := http.StatusInternalServerError, "internal"
	switch {
	case errors.Is(err, errInvalidID):
		status, code = http.StatusBadRequest, "invalid_id"
	case errors.Is(err, errNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, errCorrupt):
		status, code = http.StatusUnprocessableEntity, "corrupt_backup"
	case errors.Is(err, errIncompatible):
		status, code = http.StatusConflict, "incompatible_backup"
	default:
		log.WithError(err).Error("taproot backups: request failed")
	}
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": err.Error()}})
}
