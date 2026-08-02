package compile

import "rotor/tsgo/core"

func ApplyCheckerOverride(options *core.CompilerOptions, checkers *int) {
	if checkers != nil {
		options.Checkers = checkers
	}
}

func applyCheckerOverride(options *core.CompilerOptions, checkers *int) {
	ApplyCheckerOverride(options, checkers)
}
