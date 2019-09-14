package golinters

import (
	"golang.org/x/tools/go/analysis"

	"github.com/golangci/golangci-lint/pkg/config"

	"github.com/golangci/golangci-lint/pkg/golinters/goanalysis"

	// analysis plug-ins
	"golang.org/x/tools/go/analysis/passes/asmdecl"
	"golang.org/x/tools/go/analysis/passes/assign"
	"golang.org/x/tools/go/analysis/passes/atomic"
	"golang.org/x/tools/go/analysis/passes/atomicalign"
	"golang.org/x/tools/go/analysis/passes/bools"
	"golang.org/x/tools/go/analysis/passes/buildtag"
	"golang.org/x/tools/go/analysis/passes/cgocall"
	"golang.org/x/tools/go/analysis/passes/composite"
	"golang.org/x/tools/go/analysis/passes/copylock"
	"golang.org/x/tools/go/analysis/passes/errorsas"
	"golang.org/x/tools/go/analysis/passes/httpresponse"
	"golang.org/x/tools/go/analysis/passes/loopclosure"
	"golang.org/x/tools/go/analysis/passes/lostcancel"
	"golang.org/x/tools/go/analysis/passes/nilfunc"
	"golang.org/x/tools/go/analysis/passes/printf"
	"golang.org/x/tools/go/analysis/passes/shadow"
	"golang.org/x/tools/go/analysis/passes/shift"
	"golang.org/x/tools/go/analysis/passes/stdmethods"
	"golang.org/x/tools/go/analysis/passes/structtag"
	"golang.org/x/tools/go/analysis/passes/tests"
	"golang.org/x/tools/go/analysis/passes/unmarshal"
	"golang.org/x/tools/go/analysis/passes/unreachable"
	"golang.org/x/tools/go/analysis/passes/unsafeptr"
	"golang.org/x/tools/go/analysis/passes/unusedresult"
)

func getAllAnalyzers() []*analysis.Analyzer {
	return []*analysis.Analyzer{
		asmdecl.Analyzer,
		assign.Analyzer,
		atomic.Analyzer,
		atomicalign.Analyzer,
		bools.Analyzer,
		buildtag.Analyzer,
		cgocall.Analyzer,
		composite.Analyzer,
		copylock.Analyzer,
		errorsas.Analyzer,
		httpresponse.Analyzer,
		loopclosure.Analyzer,
		lostcancel.Analyzer,
		nilfunc.Analyzer,
		printf.Analyzer,
		shadow.Analyzer,
		shift.Analyzer,
		stdmethods.Analyzer,
		structtag.Analyzer,
		tests.Analyzer,
		unmarshal.Analyzer,
		unreachable.Analyzer,
		unsafeptr.Analyzer,
		unusedresult.Analyzer,
	}
}

func getDefaultAnalyzers() []*analysis.Analyzer {
	return []*analysis.Analyzer{
		asmdecl.Analyzer,
		assign.Analyzer,
		atomic.Analyzer,
		bools.Analyzer,
		buildtag.Analyzer,
		cgocall.Analyzer,
		composite.Analyzer,
		copylock.Analyzer,
		errorsas.Analyzer,
		httpresponse.Analyzer,
		loopclosure.Analyzer,
		lostcancel.Analyzer,
		nilfunc.Analyzer,
		printf.Analyzer,
		shift.Analyzer,
		stdmethods.Analyzer,
		structtag.Analyzer,
		tests.Analyzer,
		unmarshal.Analyzer,
		unreachable.Analyzer,
		unsafeptr.Analyzer,
		unusedresult.Analyzer,
	}
}

func isAnalyzerEnabled(name string, cfg *config.GovetSettings, defaultAnalyzers []*analysis.Analyzer) bool {
	if cfg.EnableAll {
		return true
	}
	// Raw for loops should be OK on small slice lengths.
	for _, n := range cfg.Enable {
		if n == name {
			return true
		}
	}
	for _, n := range cfg.Disable {
		if n == name {
			return true
		}
	}
	if cfg.DisableAll {
		return false
	}
	for _, a := range defaultAnalyzers {
		if a.Name == name {
			return true
		}
	}
	return false
}

func analyzersFromConfig(cfg *config.GovetSettings) []*analysis.Analyzer {
	if cfg == nil {
		return getDefaultAnalyzers()
	}
	if cfg.CheckShadowing {
		// Keeping for backward compatibility.
		cfg.Enable = append(cfg.Enable, shadow.Analyzer.Name)
	}

	var enabledAnalyzers []*analysis.Analyzer
	defaultAnalyzers := getDefaultAnalyzers()
	for _, a := range getAllAnalyzers() {
		if isAnalyzerEnabled(a.Name, cfg, defaultAnalyzers) {
			enabledAnalyzers = append(enabledAnalyzers, a)
		}
	}

	return enabledAnalyzers
}

func NewGovet(cfg *config.GovetSettings) *goanalysis.Linter {
	var settings map[string]map[string]interface{}
	if cfg != nil {
		settings = cfg.Settings
	}
	return goanalysis.NewLinter(
		"govet",
		"Vet examines Go source code and reports suspicious constructs, "+
			"such as Printf calls whose arguments do not align with the format string",
		analyzersFromConfig(cfg),
		settings,
	)
}
