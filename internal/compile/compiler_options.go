package compile

import "rotor/tsgo/core"

func applyCheckerOverride(options *core.CompilerOptions, checkers *int) {
	if checkers != nil {
		options.Checkers = checkers
	}
}
