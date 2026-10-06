package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/tend"
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
	fs := newFlags("edit")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	s, err := tend.Open()
	if err != nil {
		return err
	}
	r, err := remotePick(s, first(pos))
	if err != nil {
		return err
	}
	r = fresh(r)

	before := editable{r.Title, r.Label, r.Summary, r.Tags, r.Project, r.WorkType, r.Status}
	raw, err := json.MarshalIndent(before, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp("", "tend-edit-*.json")
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
	if !tend.ValidStatus(after.Status) {
		return i18n.E("cli.edit.bad_status", strings.Join(tend.Statuses, "|"), after.Status)
	}

	p := tend.Patch{Title: &after.Title, Label: &after.Label, Summary: &after.Summary, Tags: &after.Tags,
		Project: &after.Project, WorkType: &after.WorkType, Status: &after.Status}
	r, err = writeRec(s, r, p, &r.UpdatedAt)
	if err != nil {
		return err
	}
	fmt.Print(i18n.F("cli.edit.updated", r.Title))
	return nil
}

func openEditor(path string) error {
	parts := shell.Editor()
	if len(parts) == 0 {
		return errors.New(i18n.T("cli.edit.no_editor"))
	}
	bin, err := exec.LookPath(parts[0])
	if err != nil {
		return i18n.E("cli.edit.editor_missing", parts[0], err)
	}
	cmd := exec.Command(bin, append(parts[1:], filepath.Clean(path))...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
