package processors

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"

	"github.com/golangci/golangci-lint/pkg/logutils"
	"github.com/golangci/golangci-lint/pkg/result"
)

var autogenDebugf = logutils.Debug("autogen_exclude")

type ageFileSummary struct {
	isGenerated bool
}

type ageFileSummaryCache map[string]*ageFileSummary

type AutogeneratedExclude struct {
	fileSummaryCache ageFileSummaryCache
}

func NewAutogeneratedExclude() *AutogeneratedExclude {
	return &AutogeneratedExclude{
		fileSummaryCache: ageFileSummaryCache{},
	}
}

var _ Processor = &AutogeneratedExclude{}

func (p AutogeneratedExclude) Name() string {
	return "autogenerated_exclude"
}

func (p *AutogeneratedExclude) Process(issues []result.Issue) ([]result.Issue, error) {
	return filterIssuesErr(issues, p.shouldPassIssue)
}

func isSpecialAutogeneratedFile(filePath string) bool {
	fileName := filepath.Base(filePath)
	// fake files to which //line points to for goyacc generated files
	return fileName == "yacctab" || fileName == "yaccpar" || fileName == "NONE"
}

func (p *AutogeneratedExclude) shouldPassIssue(i *result.Issue) (bool, error) {
	if i.FromLinter == "typecheck" {
		// don't hide typechecking errors in generated files: users expect to see why the project isn't compiling
		return true, nil
	}

	if isSpecialAutogeneratedFile(i.FilePath()) {
		return false, nil
	}

	fs, err := p.getOrCreateFileSummary(i)
	if err != nil {
		return false, err
	}

	// don't report issues for autogenerated files
	return !fs.isGenerated, nil
}

// isGenerated reports whether the source file is generated code.
// Using a bit laxer rules than https://golang.org/s/generatedcode to
// match more generated code. See #48 and #72.
func isGeneratedFileByComment(doc string) bool {
	const (
		genCodeGenerated = "code generated"
		genDoNotEdit     = "do not edit"
		genAutoFile      = "autogenerated file" // easyjson
	)

	markers := []string{genCodeGenerated, genDoNotEdit, genAutoFile}
	doc = strings.ToLower(doc)
	for _, marker := range markers {
		if strings.Contains(doc, marker) {
			autogenDebugf("doc contains marker %q: file is generated", marker)
			return true
		}
	}

	autogenDebugf("doc of len %d doesn't contain any of markers: %s", len(doc), markers)
	return false
}

func (p *AutogeneratedExclude) getOrCreateFileSummary(i *result.Issue) (*ageFileSummary, error) {
	fs := p.fileSummaryCache[i.FilePath()]
	if fs != nil {
		return fs, nil
	}

	fs = &ageFileSummary{}
	p.fileSummaryCache[i.FilePath()] = fs

	if i.FilePath() == "" {
		return nil, fmt.Errorf("no file path for issue")
	}

	doc, err := getDoc(i.FilePath())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get doc of file %s", i.FilePath())
	}

	fs.isGenerated = isGeneratedFileByComment(doc)
	autogenDebugf("file %q is generated: %t", i.FilePath(), fs.isGenerated)
	return fs, nil
}

func getDoc(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", errors.Wrap(err, "failed to open file")
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	// Issue 954: Some lines can be very long, e.g. auto-generated
	// embedded resources. Reported on file of 86.2KB.
	const maxTokenSize = 512 * 1024 // 512KB should be enough
	scanner.Buffer(make([]byte, maxTokenSize), maxTokenSize)

	var docLines []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "//") { //nolint:gocritic
			text := strings.TrimSpace(strings.TrimPrefix(line, "//"))
			docLines = append(docLines, text)
		} else if line == "" || strings.HasPrefix(line, "package") {
			// go to next line
		} else {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return "", errors.Wrap(err, "failed to scan file")
	}

	return strings.Join(docLines, "\n"), nil
}

func (p AutogeneratedExclude) Finish() {}
