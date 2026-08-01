package compile

import "path/filepath"

type solutionBuildDrainer struct {
	importPathMap map[string]string
	persists      []func()
}

func (d *solutionBuildDrainer) Drain(project SolutionProject) (*BuildResult, []string, error) {
	options := project.Options
	options.TsConfigPath = project.ConfigPath
	options.crossProjectImportPathMap = d.importPathMap
	options.pendingSolutionPersists = &d.persists
	options.deferRojoCachePersist = true
	return BuildProjectWithOptions(filepath.Dir(project.ConfigPath), options)
}

func (d *solutionBuildDrainer) persist() {
	for _, persist := range d.persists {
		persist()
	}
	d.persists = nil
}
