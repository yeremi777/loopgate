package job

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yeremi777/loopgate/internal/verify"
)

// StageRow is one ended stage of a Job: a Claude Code stage with its
// reported status, a Verify gate with its commands and, when it failed, the
// tail of its output, or the publish of the draft PR.
type StageRow struct {
	Stage    string           `json:"stage"`
	Start    time.Time        `json:"start"`
	End      time.Time        `json:"end"`
	Status   string           `json:"status"`
	CostUSD  float64          `json:"cost_usd"`
	Commands []verify.Command `json:"commands,omitempty"`
	Output   []string         `json:"output,omitempty"`
}

const stagesFile = "stages.jsonl"

// appendStage adds row to the stage rows in the Job's directory.
func appendStage(dir string, row StageRow) error {
	data, err := json.Marshal(row)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, stagesFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

// ReadStages is the stage rows in a Job's directory, oldest first; a Job
// that never ended a stage has none.
func ReadStages(dir string) ([]StageRow, error) {
	f, err := os.Open(filepath.Join(dir, stagesFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var rows []StageRow
	lines := bufio.NewScanner(f)
	lines.Buffer(nil, 1<<20)
	for lines.Scan() {
		var row StageRow
		if err := json.Unmarshal(lines.Bytes(), &row); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", stagesFile, len(rows)+1, err)
		}
		rows = append(rows, row)
	}
	return rows, lines.Err()
}
