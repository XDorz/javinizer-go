package database

import (
	"time"

	"gorm.io/gorm"

	"github.com/javinizer/javinizer-go/internal/models"
)

func loadCreditReassignmentsTx(tx *gorm.DB, movieContentID string) (map[uint]uint, error) {
	var rows []models.MovieCreditReassignment
	if err := tx.Where("movie_content_id = ?", movieContentID).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[uint]uint, len(rows))
	for _, row := range rows {
		result[row.SourceActressID] = row.TargetActressID
	}
	return result, nil
}

func recordCreditReassignmentTx(tx *gorm.DB, movieContentID string, sourceActressID, targetActressID uint) error {
	now := time.Now().UTC()
	if err := tx.Model(&models.MovieCreditReassignment{}).
		Where("movie_content_id = ? AND target_actress_id = ?", movieContentID, sourceActressID).
		Updates(map[string]interface{}{"target_actress_id": targetActressID, "updated_at": now}).Error; err != nil {
		return err
	}
	return tx.Exec(`
		INSERT INTO movie_credit_reassignments (movie_content_id, source_actress_id, target_actress_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(movie_content_id, source_actress_id) DO UPDATE SET
			target_actress_id = excluded.target_actress_id,
			updated_at = excluded.updated_at`,
		movieContentID, sourceActressID, targetActressID, now, now).Error
}
