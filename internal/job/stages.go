package job

import (
	"bufio"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/yeremi777/loopgate/internal/verify"
)

// StageRow is one ended stage of a Job: a Claude Code stage with its
// reported status, a Verify gate with its commands and, when it failed, the
// tail of its output, or the publish of the draft PR. Bound is the bound
// that ran out, iterations or timeout, on the last row of an exhausted Job.
type StageRow struct {
	Stage    string           `json:"stage"`
	Start    time.Time        `json:"start"`
	End      time.Time        `json:"end"`
	Status   string           `json:"status"`
	CostUSD  float64          `json:"cost_usd"`
	Commands []verify.Command `json:"commands,omitempty"`
	Output   []string         `json:"output,omitempty"`
	Bound    string           `json:"bound,omitempty"`
}

const stagesFile = "stages.jsonl"

// appendStage adds row to the stage rows in the Job's directory, on a line
// of its own even when a crash left the file ending mid-line.
func appendStage(dir string, row StageRow) error {
	data, err := json.Marshal(row)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, stagesFile), os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	midLine, err := endsMidLine(f)
	if err != nil {
		return errors.Join(err, f.Close())
	}
	if midLine {
		data = append([]byte{'\n'}, data...)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

func endsMidLine(f *os.File) (bool, error) {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return false, err
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], info.Size()-1); err != nil {
		return false, err
	}
	return last[0] != '\n', nil
}

// markBound names bound on the last stage row in the Job's directory, when
// it has one, and swaps the file in whole so a reader never sees half of it.
func markBound(dir, bound string) error {
	rows, err := ReadStages(dir)
	if err != nil || len(rows) == 0 {
		return err
	}
	rows[len(rows)-1].Bound = bound
	var data []byte
	for _, row := range rows {
		line, err := json.Marshal(row)
		if err != nil {
			return err
		}
		data = append(append(data, line...), '\n')
	}
	f, err := os.CreateTemp(dir, stagesFile+".*")
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return errors.Join(err, f.Close(), os.Remove(f.Name()))
	}
	if err := f.Close(); err != nil {
		return errors.Join(err, os.Remove(f.Name()))
	}
	if err := os.Rename(f.Name(), filepath.Join(dir, stagesFile)); err != nil {
		return errors.Join(err, os.Remove(f.Name()))
	}
	return nil
}

// ReadStages is the stage rows in a Job's directory, oldest first; a Job
// that never ended a stage has none. A line that is not a row, such as one
// cut short by a crash, is logged and left out.
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
	for n := 1; lines.Scan(); n++ {
		var row StageRow
		if err := json.Unmarshal(lines.Bytes(), &row); err != nil {
			log.Printf("%s line %d: skipped: %v", filepath.Join(dir, stagesFile), n, err)
			continue
		}
		rows = append(rows, row)
	}
	return rows, lines.Err()
}
