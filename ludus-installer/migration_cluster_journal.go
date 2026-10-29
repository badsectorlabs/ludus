package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

type migrationNetworkWriteJournal map[string]map[string][]migrationSavedFile

func (m *Migration) writeNodeFile(ctx context.Context, n migrationNodeIO, path string, data []byte, mode uint32) error {
	relative, err := filepath.Rel(migrationNetworkRoot, path)
	if err != nil {
		return err
	}
	if m.NetworkWrites == nil {
		m.NetworkWrites = migrationNetworkWriteJournal{}
	}
	if m.NetworkWrites[n.Node.Name] == nil {
		m.NetworkWrites[n.Node.Name] = map[string][]migrationSavedFile{}
	}
	// Record before attempting the atomic write: cancellation can arrive after the
	// rename succeeds but before SSH delivers its exit status.
	m.NetworkWrites[n.Node.Name][relative] = append(m.NetworkWrites[n.Node.Name][relative], migrationSavedFile{Data: data, Mode: mode})
	if err = m.save(); err != nil {
		return err
	}
	return n.write(ctx, path, data, mode)
}
func migrationRestoreFileDecision(original *migrationSavedFile, versions []migrationSavedFile, current []byte, exists bool) (bool, error) {
	if original == nil && !exists {
		return false, nil
	}
	if original != nil && exists && bytes.Equal(original.Data, current) {
		return false, nil
	}
	if !exists {
		return false, errors.New("network configuration was removed independently")
	}
	for _, version := range versions {
		if bytes.Equal(version.Data, current) {
			return true, nil
		}
	}
	return false, errors.New("network configuration changed independently; refusing to overwrite")
}
func (m *Migration) restoreNodeFiles(ctx context.Context, n migrationNodeIO) error {
	var failures []error
	saved := m.Network.Snapshots[n.Node.Name]
	for name, versions := range m.NetworkWrites[n.Node.Name] {
		path := filepath.Join(migrationNetworkRoot, name)
		exists := n.exists(ctx, path)
		var data []byte
		var err error
		if exists {
			data, err = n.read(ctx, path)
			if err != nil {
				failures = append(failures, err)
				continue
			}
		}
		var original *migrationSavedFile
		if item, ok := saved.Files[name]; ok {
			original = &item
		}
		restore, err := migrationRestoreFileDecision(original, versions, data, exists)
		if err != nil {
			failures = append(failures, fmt.Errorf("node %s %s: %w", n.Node.Name, name, err))
			continue
		}
		if !restore {
			continue
		}
		if original != nil {
			err = n.write(ctx, path, original.Data, original.Mode)
		} else {
			err = n.run(ctx, "rm", "-f", "--", path)
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
