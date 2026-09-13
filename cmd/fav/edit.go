package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
)

type editable struct {
	Title    string   `json:"title"`
	Label    string   `json:"label"`
	Summary  string   `json:"summary"`
	Tags     []string `json:"tags"`
	Project  string   `json:"project"`
	WorkType string   `json:"work_type"`
	Status   string   `json:"status"`
}

func cmdEdit(args []string) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	r, err := pick(s, firstArg(args))
	if err != nil {
		return err
	}

	before := editable{r.Title, r.Label, r.Summary, r.Tags, r.Project, r.WorkType, r.Status}
	raw, err := json.MarshalIndent(before, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp("", "fav-edit-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	if err := openEditor(tmp.Name()); err != nil {
		return err
	}

	edited, err := os.ReadFile(tmp.Name())
	if err != nil {
		return err
	}
	var after editable
	if err := json.Unmarshal(edited, &after); err != nil {
		return i18n.E("cli.edit.invalid_json", err)
	}
	if strings.TrimSpace(after.Title) == "" || strings.TrimSpace(after.Summary) == "" {
		return errors.New(i18n.T("cli.edit.empty_fields"))
	}
	if !fav.ValidStatus(after.Status) {
		return i18n.E("cli.edit.bad_status", strings.Join(fav.Statuses, "|"), after.Status)
	}

	r.Title = strings.TrimSpace(after.Title)
	r.Label = strings.TrimSpace(after.Label)
	r.Summary = strings.TrimSpace(after.Summary)
	r.Tags = fav.Normalize(after.Tags)
	r.Project, r.WorkType, r.Status = after.Project, after.WorkType, after.Status
	if err := save(s, r); err != nil {
		return err
	}
	fmt.Print(i18n.F("cli.edit.updated", r.Title))
	return nil
}

func openEditor(path string) error {
	ed := os.Getenv("VISUAL")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	if ed == "" {
		return errors.New(i18n.T("cli.edit.no_editor"))
	}
	// $EDITOR may carry arguments ("code -w"): split on whitespace, no shell.
	parts := strings.Fields(ed)
	bin, err := exec.LookPath(parts[0])
	if err != nil {
		return i18n.E("cli.edit.editor_missing", parts[0], err)
	}
	cmd := exec.Command(bin, append(parts[1:], filepath.Clean(path))...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
