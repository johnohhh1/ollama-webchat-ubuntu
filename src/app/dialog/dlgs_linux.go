//go:build linux

package dialog

import (
	"os/exec"
	"strings"
)

// yesNo uses zenity for a yes/no dialog on Linux.
func (b *MsgBuilder) yesNo() bool {
	args := []string{"--question", "--text=" + b.Msg}
	if b.Dlg.Title != "" {
		args = append(args, "--title="+b.Dlg.Title)
	}
	err := exec.Command("zenity", args...).Run()
	return err == nil
}

func (b *MsgBuilder) info() {
	args := []string{"--info", "--text=" + b.Msg}
	if b.Dlg.Title != "" {
		args = append(args, "--title="+b.Dlg.Title)
	}
	exec.Command("zenity", args...).Run() //nolint:errcheck
}

func (b *MsgBuilder) error() {
	args := []string{"--error", "--text=" + b.Msg}
	if b.Dlg.Title != "" {
		args = append(args, "--title="+b.Dlg.Title)
	}
	exec.Command("zenity", args...).Run() //nolint:errcheck
}

// buildZenityFileArgs constructs common --file-selection args.
func (b *FileBuilder) buildZenityFileArgs(save bool) []string {
	args := []string{"--file-selection"}
	if save {
		args = append(args, "--save", "--confirm-overwrite")
	}
	if b.Dlg.Title != "" {
		args = append(args, "--title="+b.Dlg.Title)
	}
	if b.StartDir != "" {
		args = append(args, "--filename="+b.StartDir+"/")
	}
	if b.ShowHiddenFiles {
		args = append(args, "--hidden")
	}
	for _, f := range b.Filters {
		for _, ext := range f.Extensions {
			if ext == "*" {
				continue
			}
			args = append(args, "--file-filter="+f.Desc+" | *."+ext)
		}
	}
	return args
}

func (b *FileBuilder) load() (string, error) {
	out, err := exec.Command("zenity", b.buildZenityFileArgs(false)...).Output()
	if err != nil {
		return "", ErrCancelled
	}
	return strings.TrimSpace(string(out)), nil
}

func (b *FileBuilder) loadMultiple() ([]string, error) {
	args := append(b.buildZenityFileArgs(false), "--multiple", "--separator=\n")
	out, err := exec.Command("zenity", args...).Output()
	if err != nil {
		return nil, ErrCancelled
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, ErrCancelled
	}
	return strings.Split(raw, "\n"), nil
}

func (b *FileBuilder) save() (string, error) {
	out, err := exec.Command("zenity", b.buildZenityFileArgs(true)...).Output()
	if err != nil {
		return "", ErrCancelled
	}
	return strings.TrimSpace(string(out)), nil
}

func (b *DirectoryBuilder) browse() (string, error) {
	args := []string{"--file-selection", "--directory"}
	if b.Dlg.Title != "" {
		args = append(args, "--title="+b.Dlg.Title)
	}
	if b.StartDir != "" {
		args = append(args, "--filename="+b.StartDir+"/")
	}
	if b.ShowHiddenFiles {
		args = append(args, "--hidden")
	}
	out, err := exec.Command("zenity", args...).Output()
	if err != nil {
		return "", ErrCancelled
	}
	return strings.TrimSpace(string(out)), nil
}
