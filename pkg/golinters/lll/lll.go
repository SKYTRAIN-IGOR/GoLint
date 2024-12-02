package lll

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"go/token"
	"os"
	"sync"
	"unicode/utf8"

	"golang.org/x/tools/go/analysis"

	"github.com/golangci/golangci-lint/pkg/config"
	"github.com/golangci/golangci-lint/pkg/goanalysis"
	"github.com/golangci/golangci-lint/pkg/golinters/internal"
	"github.com/golangci/golangci-lint/pkg/lint/linter"
	"github.com/golangci/golangci-lint/pkg/result"
)

const linterName = "lll"

var goCommentDirectivePrefix = []byte("//go:")

func New(settings *config.LllSettings) *goanalysis.Linter {
	var mu sync.Mutex
	var resIssues []goanalysis.Issue

	analyzer := &analysis.Analyzer{
		Name: linterName,
		Doc:  goanalysis.TheOnlyanalyzerDoc,
		Run: func(pass *analysis.Pass) (any, error) {
			issues, err := runLll(pass, settings)
			if err != nil {
				return nil, err
			}

			if len(issues) == 0 {
				return nil, nil
			}

			mu.Lock()
			resIssues = append(resIssues, issues...)
			mu.Unlock()

			return nil, nil
		},
	}

	return goanalysis.NewLinter(
		linterName,
		"Reports long lines",
		[]*analysis.Analyzer{analyzer},
		nil,
	).WithIssuesReporter(func(*linter.Context) []goanalysis.Issue {
		return resIssues
	}).WithLoadMode(goanalysis.LoadModeSyntax)
}

func runLll(pass *analysis.Pass, settings *config.LllSettings) ([]goanalysis.Issue, error) {
	fileNames := internal.GetFileNames(pass)

	spaces := bytes.Repeat([]byte{' '}, settings.TabWidth)

	var issues []goanalysis.Issue
	for _, f := range fileNames {
		lintIssues, err := getLLLIssuesForFile(f, settings.LineLength, spaces)
		if err != nil {
			return nil, err
		}

		for i := range lintIssues {
			issues = append(issues, goanalysis.NewIssue(&lintIssues[i], pass))
		}
	}

	return issues, nil
}

func getLLLIssuesForFile(filename string, maxLineLen int, tabSpaces []byte) ([]result.Issue, error) {
	var res []result.Issue

	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("can't open file %s: %w", filename, err)
	}
	defer f.Close()

	lineNumber := 1
	multiImportEnabled := false

	scanner := bufio.NewScanner(f)
	for ; scanner.Scan(); lineNumber++ {
		line := scanner.Bytes()

		if bytes.HasPrefix(line, goCommentDirectivePrefix) {
			continue
		}

		if bytes.HasPrefix(line, []byte("import")) {
			multiImportEnabled = bytes.HasSuffix(line, []byte{'('})
			continue
		}

		if multiImportEnabled {
			if bytes.Equal(line, []byte{')'}) {
				multiImportEnabled = false
			}

			continue
		}

		line = bytes.ReplaceAll(line, []byte{'\t'}, tabSpaces)

		lineLen := utf8.RuneCount(line)
		if lineLen > maxLineLen {
			res = append(res, result.Issue{
				Pos: token.Position{
					Filename: filename,
					Line:     lineNumber,
				},
				Text:       fmt.Sprintf("the line is %d characters long, which exceeds the maximum of %d characters.", lineLen, maxLineLen),
				FromLinter: linterName,
			})
		}
	}

	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) && maxLineLen < bufio.MaxScanTokenSize {
			// scanner.Scan() might fail if the line is longer than bufio.MaxScanTokenSize
			// In the case where the specified maxLineLen is smaller than bufio.MaxScanTokenSize
			// we can return this line as a long line instead of returning an error.
			// The reason for this change is that this case might happen with autogenerated files
			// The go-bindata tool for instance might generate a file with a very long line.
			// In this case, as it's an auto generated file, the warning returned by lll will
			// be ignored.
			// But if we return a linter error here, and this error happens for an autogenerated
			// file the error will be discarded (fine), but all the subsequent errors for lll will
			// be discarded for other files, and we'll miss legit error.
			res = append(res, result.Issue{
				Pos: token.Position{
					Filename: filename,
					Line:     lineNumber,
					Column:   1,
				},
				Text:       fmt.Sprintf("line is more than %d characters", bufio.MaxScanTokenSize),
				FromLinter: linterName,
			})
		} else {
			return nil, fmt.Errorf("can't scan file %s: %w", filename, err)
		}
	}

	return res, nil
}
