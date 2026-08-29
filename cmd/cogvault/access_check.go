package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/config"
	"golang.org/x/sys/unix"
)

type accessCheckOps struct {
	readDir  func(string) ([]os.DirEntry, error)
	lstat    func(string) (os.FileInfo, error)
	open     func(string, int, uint32) (int, error)
	read     func(*os.File, []byte) (int, error)
	write    func(*os.File, []byte) (int, error)
	close    func(*os.File) error
	readFile func(string) ([]byte, error)
	remove   func(string) error
}

var defaultAccessCheckOps = accessCheckOps{
	readDir:  os.ReadDir,
	lstat:    os.Lstat,
	open:     unix.Open,
	read:     func(file *os.File, b []byte) (int, error) { return file.Read(b) },
	write:    func(file *os.File, b []byte) (int, error) { return file.Write(b) },
	close:    func(file *os.File) error { return file.Close() },
	readFile: os.ReadFile,
	remove:   os.Remove,
}

func newAccessCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "access-check",
		Short: "Verify access to configured ingest paths",
		Args:  cobra.NoArgs,
		RunE:  runAccessCheck,
	}
}

func runAccessCheck(cmd *cobra.Command, _ []string) error {
	configPath, err := resolveConfigPath(cmd)
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	ops := defaultAccessCheckOps

	writeSurfaces := []struct {
		name string
		path string
	}{
		{name: "wiki_dir", path: cfg.WikiDir},
		{name: "db_parent", path: filepath.Dir(cfg.DBPath)},
	}
	for _, surface := range writeSurfaces {
		if err := probeWriteSurface(surface.name, surface.path, ops); err != nil {
			return err
		}
		cmd.Printf("passed: %s: %s\n", surface.name, surface.path)
	}
	for _, source := range cfg.Sources {
		if err := probeSource(source, int64(cfg.MaxFileSizeMB)<<20, ops); err != nil {
			return err
		}
		cmd.Printf("passed: source: %s\n", source.Path)
	}
	cmd.Println("configured ingest access check passed")
	return nil
}

func probeWriteSurface(surface, dir string, ops accessCheckOps) (result error) {
	info, err := ops.lstat(dir)
	if err != nil {
		return fmt.Errorf("%s %s: lstat: %w", surface, dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s %s: not a directory", surface, dir)
	}
	file, err := os.CreateTemp(dir, ".cogvault-access-check-*")
	if err != nil {
		return fmt.Errorf("%s %s: create sentinel: %w", surface, dir, err)
	}
	path := file.Name()
	defer func() {
		if removeErr := ops.remove(path); removeErr != nil {
			result = errors.Join(result, fmt.Errorf("%s %s: remove sentinel %s: %w", surface, dir, path, removeErr))
		}
	}()
	payload := []byte("cogvault-access-check")
	if _, err := ops.write(file, payload); err != nil {
		_ = ops.close(file)
		return fmt.Errorf("%s %s: write sentinel: %w", surface, dir, err)
	}
	if err := ops.close(file); err != nil {
		return fmt.Errorf("%s %s: close sentinel: %w", surface, dir, err)
	}
	got, err := ops.readFile(path)
	if err != nil {
		return fmt.Errorf("%s %s: read sentinel: %w", surface, dir, err)
	}
	if string(got) != string(payload) {
		return fmt.Errorf("%s %s: compare sentinel: content mismatch", surface, dir)
	}
	return nil
}

func probeSource(source config.SourceDir, maxSize int64, ops accessCheckOps) error {
	entries, err := ops.readDir(source.Path)
	if err != nil {
		return fmt.Errorf("source %s: read directory: %w", source.Path, err)
	}
	allowed := make(map[string]struct{}, len(source.Types))
	for _, fileType := range source.Types {
		allowed[strings.ToLower(strings.TrimPrefix(fileType, "."))] = struct{}{}
	}
	for _, entry := range entries {
		path := filepath.Join(source.Path, entry.Name())
		before, err := ops.lstat(path)
		if err != nil {
			return fmt.Errorf("source %s: lstat %s: %w", source.Path, path, err)
		}
		if !before.Mode().IsRegular() || before.Size() > maxSize {
			continue
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(entry.Name()), "."))
		if _, ok := allowed[ext]; !ok {
			continue
		}
		fd, err := ops.open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("source %s: open %s: %w", source.Path, path, err)
		}
		file := os.NewFile(uintptr(fd), path)
		if file == nil {
			_ = unix.Close(fd)
			return fmt.Errorf("source %s: open %s: invalid descriptor", source.Path, path)
		}
		after, statErr := file.Stat()
		if statErr == nil && (!after.Mode().IsRegular() || !os.SameFile(before, after)) {
			statErr = errors.New("file identity changed")
		}
		if statErr != nil {
			_ = file.Close()
			return fmt.Errorf("source %s: verify %s: %w", source.Path, path, statErr)
		}
		var one [1]byte
		_, readErr := ops.read(file, one[:])
		closeErr := file.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("source %s: read %s: %w", source.Path, path, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("source %s: close %s: %w", source.Path, path, closeErr)
		}
	}
	return nil
}
