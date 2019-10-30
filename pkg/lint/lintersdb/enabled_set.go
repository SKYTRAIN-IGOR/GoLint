package lintersdb

import (
	"fmt"
	"golang.org/x/tools/go/analysis"
	"plugin"
	"sort"

	"github.com/golangci/golangci-lint/pkg/config"
	"github.com/golangci/golangci-lint/pkg/golinters/goanalysis"
	"github.com/golangci/golangci-lint/pkg/lint/linter"
	"github.com/golangci/golangci-lint/pkg/logutils"
)

type EnabledSet struct {
	m      *Manager
	v      *Validator
	log    logutils.Log
	cfg    *config.Config
	debugf logutils.DebugFunc
}

func NewEnabledSet(m *Manager, v *Validator, log logutils.Log, cfg *config.Config) *EnabledSet {
	return &EnabledSet{
		m:      m,
		v:      v,
		log:    log,
		cfg:    cfg,
		debugf: logutils.Debug("enabled_linters"),
	}
}

func (es EnabledSet) build(lcfg *config.Linters, enabledByDefaultLinters []*linter.Config) map[string]*linter.Config {
	resultLintersSet := map[string]*linter.Config{}
	switch {
	case len(lcfg.Presets) != 0:
		break // imply --disable-all
	case lcfg.EnableAll:
		resultLintersSet = linterConfigsToMap(es.m.GetAllSupportedLinterConfigs())
	case lcfg.DisableAll:
		break
	default:
		resultLintersSet = linterConfigsToMap(enabledByDefaultLinters)
	}

	// --presets can only add linters to default set
	for _, p := range lcfg.Presets {
		for _, lc := range es.m.GetAllLinterConfigsForPreset(p) {
			lc := lc
			resultLintersSet[lc.Name()] = lc
		}
	}

	// --fast removes slow linters from current set.
	// It should be after --presets to be able to run only fast linters in preset.
	// It should be before --enable and --disable to be able to enable or disable specific linter.
	if lcfg.Fast {
		for name, lc := range resultLintersSet {
			if lc.IsSlowLinter() {
				delete(resultLintersSet, name)
			}
		}
	}

	for _, name := range lcfg.Enable {
		for _, lc := range es.m.GetLinterConfigs(name) {
			// it's important to use lc.Name() nor name because name can be alias
			resultLintersSet[lc.Name()] = lc
		}
	}

	for _, name := range lcfg.Disable {
		for _, lc := range es.m.GetLinterConfigs(name) {
			// it's important to use lc.Name() nor name because name can be alias
			delete(resultLintersSet, lc.Name())
		}
	}

	// This isn't quite the right place to do this, but proves it can be done
	for name, settings := range es.cfg.LintersSettings.Custom {
		linterConfig, err := es.loadCustomLinterConfig(name, settings)

		if err != nil {
			es.log.Errorf("Unable to load custom analyzer %s:%s, %v",
				name,
				settings.Path,
				err)
		} else {
			resultLintersSet[linterConfig.Name()] = linterConfig
		}
	}

	return resultLintersSet
}

func (es EnabledSet) loadCustomLinterConfig(name string, settings config.CustomLinterSettings) (*linter.Config, error) {
	analyzer, err := es.GetAnalyzerPlugin(settings.Path)
	if err != nil {
		return nil, err
	} else {
		es.log.Infof("Loaded %s: %s", settings.Path, analyzer.GetLinterName())
		customLinter := goanalysis.NewLinter(
			analyzer.GetLinterName(),
			analyzer.GetLinterDesc(),
			analyzer.GetAnalyzers(),
			nil)
		linterConfig := linter.NewConfig(customLinter)
		linterConfig.EnabledByDefault = settings.Enabled
		linterConfig.IsSlow = settings.Slow
		linterConfig.WithURL(settings.OriginalUrl)
		if name != linterConfig.Name() {
			es.log.Warnf("Configuration linter name %s doesn't match plugin linter name %s", name, linterConfig.Name())
		}
		return linterConfig, nil
	}
}

type AnalyzerPlugin interface {
	GetLinterName() string
	GetLinterDesc() string
	GetAnalyzers() []*analysis.Analyzer
}

func (es EnabledSet) GetAnalyzerPlugin(path string) (AnalyzerPlugin, error) {
	plug, err := plugin.Open(path)
	if err != nil {
		return nil, err
	}

	symbol, err := plug.Lookup("AnalyzerPlugin")
	if err != nil {
		return nil, err
	}

	analyzerPlugin, ok := symbol.(AnalyzerPlugin)
	if !ok {
		return nil, fmt.Errorf("plugin %s does not abide by 'AnalyzerPlugin' interface", path)
	}

	return analyzerPlugin, nil
}

func (es EnabledSet) Get(optimize bool) ([]*linter.Config, error) {
	if err := es.v.validateEnabledDisabledLintersConfig(&es.cfg.Linters); err != nil {
		return nil, err
	}

	resultLintersSet := es.build(&es.cfg.Linters, es.m.GetAllEnabledByDefaultLinters())
	es.verbosePrintLintersStatus(resultLintersSet)
	if optimize {
		es.combineGoAnalysisLinters(resultLintersSet)
	}

	var resultLinters []*linter.Config
	for _, lc := range resultLintersSet {
		resultLinters = append(resultLinters, lc)
	}

	return resultLinters, nil
}

func (es EnabledSet) combineGoAnalysisLinters(linters map[string]*linter.Config) {
	var goanalysisLinters []*goanalysis.Linter
	goanalysisPresets := map[string]bool{}
	for _, linter := range linters {
		lnt, ok := linter.Linter.(*goanalysis.Linter)
		if !ok {
			continue
		}
		if lnt.LoadMode() == goanalysis.LoadModeWholeProgram {
			// It's ineffective by CPU and memory to run whole-program and incremental analyzers at once.
			continue
		}
		goanalysisLinters = append(goanalysisLinters, lnt)
		for _, p := range linter.InPresets {
			goanalysisPresets[p] = true
		}
	}

	if len(goanalysisLinters) <= 1 {
		es.debugf("Didn't combine go/analysis linters: got only %d linters", len(goanalysisLinters))
		return
	}

	for _, lnt := range goanalysisLinters {
		delete(linters, lnt.Name())
	}

	ml := goanalysis.NewMetaLinter(goanalysisLinters)

	var presets []string
	for p := range goanalysisPresets {
		presets = append(presets, p)
	}

	mlConfig := &linter.Config{
		Linter:           ml,
		EnabledByDefault: false,
		InPresets:        presets,
		AlternativeNames: nil,
		OriginalURL:      "",
	}

	mlConfig = mlConfig.WithLoadForGoAnalysis()

	linters[ml.Name()] = mlConfig
	es.debugf("Combined %d go/analysis linters into one metalinter", len(goanalysisLinters))
}

func (es EnabledSet) verbosePrintLintersStatus(lcs map[string]*linter.Config) {
	var linterNames []string
	for _, lc := range lcs {
		linterNames = append(linterNames, lc.Name())
	}
	sort.StringSlice(linterNames).Sort()
	es.log.Infof("Active %d linters: %s", len(linterNames), linterNames)

	if len(es.cfg.Linters.Presets) != 0 {
		sort.StringSlice(es.cfg.Linters.Presets).Sort()
		es.log.Infof("Active presets: %s", es.cfg.Linters.Presets)
	}
}
