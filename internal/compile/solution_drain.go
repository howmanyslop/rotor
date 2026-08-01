package compile

import (
	"fmt"
	"path/filepath"
)

type solutionBuildDrainer struct {
	importPathMap map[string]string
	persists      []func() error
}

func (d *solutionBuildDrainer) Drain(project SolutionProject) (*BuildResult, []string, error) {
	options := project.Options
	options.TsConfigPath = project.ConfigPath
	options.crossProjectImportPathMap = d.importPathMap
	options.pendingSolutionPersists = &d.persists
	options.deferRojoCachePersist = true
	return BuildProjectWithOptions(filepath.Dir(project.ConfigPath), options)
}

func (d *solutionBuildDrainer) persist() error {
	for index, persist := range d.persists {
		if err := persist(); err != nil {
			d.persists = d.persists[index:]
			return fmt.Errorf("persist solution build state: %w", err)
		}
	}
	d.persists = nil
	return nil
}
