package lint

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/golangci/golangci-lint/internal/errorutil"
	"github.com/golangci/golangci-lint/pkg/config"
	"github.com/golangci/golangci-lint/pkg/fsutils"
	"github.com/golangci/golangci-lint/pkg/goutil"
	"github.com/golangci/golangci-lint/pkg/lint/linter"
	"github.com/golangci/golangci-lint/pkg/lint/lintersdb"
	"github.com/golangci/golangci-lint/pkg/logutils"
	"github.com/golangci/golangci-lint/pkg/packages"
	"github.com/golangci/golangci-lint/pkg/result"
	"github.com/golangci/golangci-lint/pkg/result/processors"
	"github.com/golangci/golangci-lint/pkg/timeutils"
)

type processorStat struct {
	inCount  int
	outCount int
}

type Runner struct {
	Log logutils.Log

	lintCtx    *linter.Context
	Processors []processors.Processor
}

func NewRunner(log logutils.Log, cfg *config.Config, goenv *goutil.Env,
	lineCache *fsutils.LineCache, fileCache *fsutils.FileCache,
	dbManager *lintersdb.Manager, lintCtx *linter.Context) (*Runner, error) {
	// Beware that some processors need to add the path prefix when working with paths
	// because they get invoked before the path prefixer (exclude and severity rules)
	// or process other paths (skip files).
	files := fsutils.NewFiles(lineCache, cfg.Output.PathPrefix)

	skipFilesProcessor, err := processors.NewSkipFiles(cfg.Run.SkipFiles, cfg.Output.PathPrefix)
	if err != nil {
		return nil, err
	}

	skipDirs := cfg.Run.SkipDirs
	if cfg.Run.UseDefaultSkipDirs {
		skipDirs = append(skipDirs, packages.StdExcludeDirRegexps...)
	}
	skipDirsProcessor, err := processors.NewSkipDirs(skipDirs, log.Child(logutils.DebugKeySkipDirs), cfg.Run.Args, cfg.Output.PathPrefix)
	if err != nil {
		return nil, err
	}

	enabledLinters, err := dbManager.GetEnabledLintersMap()
	if err != nil {
		return nil, fmt.Errorf("failed to get enabled linters: %w", err)
	}

	// print deprecated messages
	if !cfg.InternalCmdTest {
		for name, lc := range enabledLinters {
			if !lc.IsDeprecated() {
				continue
			}

			var extra string
			if lc.Deprecation.Replacement != "" {
				extra = fmt.Sprintf("Replaced by %s.", lc.Deprecation.Replacement)
			}

			log.Warnf("The linter '%s' is deprecated (since %s) due to: %s %s", name, lc.Deprecation.Since, lc.Deprecation.Message, extra)
		}
	}

	return &Runner{
		Processors: []processors.Processor{
			processors.NewCgo(goenv),

			// Must go after Cgo.
			processors.NewFilenameUnadjuster(lintCtx.Packages, log.Child(logutils.DebugKeyFilenameUnadjuster)),

			// Must be before diff, nolint and exclude autogenerated processor at least.
			processors.NewPathPrettifier(),
			skipFilesProcessor,
			skipDirsProcessor, // must be after path prettifier

			processors.NewAutogeneratedExclude(),

			// Must be before exclude because users see already marked output and configure excluding by it.
			processors.NewIdentifierMarker(),

			getExcludeProcessor(&cfg.Issues),
			getExcludeRulesProcessor(&cfg.Issues, log, files),
			processors.NewNolint(log.Child(logutils.DebugKeyNolint), dbManager, enabledLinters),

			processors.NewUniqByLine(cfg),
			processors.NewDiff(cfg.Issues.Diff, cfg.Issues.DiffFromRevision, cfg.Issues.DiffPatchFilePath, cfg.Issues.WholeFiles),
			processors.NewMaxPerFileFromLinter(cfg),
			processors.NewMaxSameIssues(cfg.Issues.MaxSameIssues, log.Child(logutils.DebugKeyMaxSameIssues), cfg),
			processors.NewMaxFromLinter(cfg.Issues.MaxIssuesPerLinter, log.Child(logutils.DebugKeyMaxFromLinter), cfg),
			processors.NewSourceCode(lineCache, log.Child(logutils.DebugKeySourceCode)),
			processors.NewPathShortener(),
			getSeverityRulesProcessor(&cfg.Severity, log, files),

			// The fixer still needs to see paths for the issues that are relative to the current directory.
			processors.NewFixer(cfg, log, fileCache),

			// Now we can modify the issues for output.
			processors.NewPathPrefixer(cfg.Output.PathPrefix),
			processors.NewSortResults(cfg),
		},
		Log: log,
	}, nil
}

func (r *Runner) Run(ctx context.Context, linters []*linter.Config) ([]result.Issue, error) {
	sw := timeutils.NewStopwatch("linters", r.Log)
	defer sw.Print()

	var (
		lintErrors error
		issues     []result.Issue
	)

	for _, lc := range linters {
		lc := lc
		sw.TrackStage(lc.Name(), func() {
			linterIssues, err := r.runLinterSafe(ctx, r.lintCtx, lc)
			if err != nil {
				lintErrors = errors.Join(lintErrors, fmt.Errorf("can't run linter %s", lc.Linter.Name()), err)
				r.Log.Warnf("Can't run linter %s: %v", lc.Linter.Name(), err)

				return
			}

			issues = append(issues, linterIssues...)
		})
	}

	return r.processLintResults(issues), lintErrors
}

func (r *Runner) runLinterSafe(ctx context.Context, lintCtx *linter.Context,
	lc *linter.Config) (ret []result.Issue, err error) {
	defer func() {
		if panicData := recover(); panicData != nil {
			if pe, ok := panicData.(*errorutil.PanicError); ok {
				err = fmt.Errorf("%s: %w", lc.Name(), pe)

				// Don't print stacktrace from goroutines twice
				r.Log.Errorf("Panic: %s: %s", pe, pe.Stack())
			} else {
				err = fmt.Errorf("panic occurred: %s", panicData)
				r.Log.Errorf("Panic stack trace: %s", debug.Stack())
			}
		}
	}()

	issues, err := lc.Linter.Run(ctx, lintCtx)

	if lc.DoesChangeTypes {
		// Packages in lintCtx might be dirty due to the last analysis,
		// which affects to the next analysis.
		// To avoid this issue, we clear type information from the packages.
		// See https://github.com/golangci/golangci-lint/pull/944.
		// Currently, DoesChangeTypes is true only for `unused`.
		lintCtx.ClearTypesInPackages()
	}

	if err != nil {
		return nil, err
	}

	for i := range issues {
		if issues[i].FromLinter == "" {
			issues[i].FromLinter = lc.Name()
		}
	}

	return issues, nil
}

func (r *Runner) processLintResults(inIssues []result.Issue) []result.Issue {
	sw := timeutils.NewStopwatch("processing", r.Log)

	var issuesBefore, issuesAfter int
	statPerProcessor := map[string]processorStat{}

	var outIssues []result.Issue
	if len(inIssues) != 0 {
		issuesBefore += len(inIssues)
		outIssues = r.processIssues(inIssues, sw, statPerProcessor)
		issuesAfter += len(outIssues)
	}

	// finalize processors: logging, clearing, no heavy work here

	for _, p := range r.Processors {
		p := p
		sw.TrackStage(p.Name(), func() {
			p.Finish()
		})
	}

	if issuesBefore != issuesAfter {
		r.Log.Infof("Issues before processing: %d, after processing: %d", issuesBefore, issuesAfter)
	}
	r.printPerProcessorStat(statPerProcessor)
	sw.PrintStages()

	return outIssues
}

func (r *Runner) printPerProcessorStat(stat map[string]processorStat) {
	parts := make([]string, 0, len(stat))
	for name, ps := range stat {
		if ps.inCount != 0 {
			parts = append(parts, fmt.Sprintf("%s: %d/%d", name, ps.outCount, ps.inCount))
		}
	}
	if len(parts) != 0 {
		r.Log.Infof("Processors filtering stat (out/in): %s", strings.Join(parts, ", "))
	}
}

func (r *Runner) processIssues(issues []result.Issue, sw *timeutils.Stopwatch, statPerProcessor map[string]processorStat) []result.Issue {
	for _, p := range r.Processors {
		var newIssues []result.Issue
		var err error
		p := p
		sw.TrackStage(p.Name(), func() {
			newIssues, err = p.Process(issues)
		})

		if err != nil {
			r.Log.Warnf("Can't process result by %s processor: %s", p.Name(), err)
		} else {
			stat := statPerProcessor[p.Name()]
			stat.inCount += len(issues)
			stat.outCount += len(newIssues)
			statPerProcessor[p.Name()] = stat
			issues = newIssues
		}

		if issues == nil {
			issues = []result.Issue{}
		}
	}

	return issues
}

func getExcludeProcessor(cfg *config.Issues) processors.Processor {
	opts := processors.ExcludeOptions{
		CaseSensitive: cfg.ExcludeCaseSensitive,
	}

	if len(cfg.ExcludePatterns) != 0 {
		opts.Pattern = fmt.Sprintf("(%s)", strings.Join(cfg.ExcludePatterns, "|"))
	}

	return processors.NewExclude(opts)
}

func getExcludeRulesProcessor(cfg *config.Issues, log logutils.Log, files *fsutils.Files) processors.Processor {
	var excludeRules []processors.ExcludeRule
	for _, r := range cfg.ExcludeRules {
		excludeRules = append(excludeRules, processors.ExcludeRule{
			BaseRule: processors.BaseRule{
				Text:       r.Text,
				Source:     r.Source,
				Path:       r.Path,
				PathExcept: r.PathExcept,
				Linters:    r.Linters,
			},
		})
	}

	if cfg.UseDefaultExcludes {
		for _, r := range config.GetExcludePatterns(cfg.IncludeDefaultExcludes) {
			excludeRules = append(excludeRules, processors.ExcludeRule{
				BaseRule: processors.BaseRule{
					Text:    r.Pattern,
					Linters: []string{r.Linter},
				},
			})
		}
	}

	opts := processors.ExcludeRulesOptions{
		Rules:         excludeRules,
		CaseSensitive: cfg.ExcludeCaseSensitive,
	}

	return processors.NewExcludeRules(log.Child(logutils.DebugKeyExcludeRules), files, opts)
}

func getSeverityRulesProcessor(cfg *config.Severity, log logutils.Log, files *fsutils.Files) processors.Processor {
	var severityRules []processors.SeverityRule
	for _, r := range cfg.Rules {
		severityRules = append(severityRules, processors.SeverityRule{
			Severity: r.Severity,
			BaseRule: processors.BaseRule{
				Text:       r.Text,
				Source:     r.Source,
				Path:       r.Path,
				PathExcept: r.PathExcept,
				Linters:    r.Linters,
			},
		})
	}

	severityOpts := processors.SeverityOptions{
		Default:       cfg.Default,
		Rules:         severityRules,
		CaseSensitive: cfg.CaseSensitive,
		Override:      !cfg.KeepLinterSeverity,
	}

	return processors.NewSeverity(log.Child(logutils.DebugKeySeverityRules), files, severityOpts)
}
