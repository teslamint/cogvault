package ingest

import (
	"errors"
	"fmt"
	"os"
)

type AttentionRow struct {
	Path        string `json:"path"`
	Status      string `json:"status"`
	Error       string `json:"error"`
	LastAttempt string `json:"last_attempt"`
	Model       string `json:"llm_model"`
	Attempts    int    `json:"attempts"`
}

func AttentionRows(dbPath, model string) ([]AttentionRow, error) {
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("ingest.AttentionRows stat %s: %w", dbPath, err)
	}

	l, err := openLedger(dbPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = l.close() }()

	rows, err := l.attentionRows(model)
	if err != nil {
		return nil, err
	}
	result := make([]AttentionRow, 0, len(rows))
	for _, row := range rows {
		status := row.status
		if status == "failed" && row.attempts >= maxAttempts {
			status = "exhausted"
		}
		result = append(result, AttentionRow{
			Path:        row.sourcePath,
			Status:      status,
			Error:       row.lastError,
			LastAttempt: row.digestedAt,
			Model:       row.llmModel,
			Attempts:    row.attempts,
		})
	}
	return result, nil
}
