package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/store"
)

type replaceInput struct {
	Comments []replaceComment `json:"comments"`
	Folds    []replaceFold    `json:"folds"`
}

type replaceComment struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	EndLine int    `json:"endLine"`
	Side    string `json:"side"`
	Origin  string `json:"origin"`
	Body    string `json:"body"`
}

type replaceFold struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	EndLine int    `json:"endLine"`
	Label   string `json:"label"`
}

func runReplace(ctx context.Context, c *CLI, args []string) error {
	fs := newFlagSet("replace")
	author := fs.String("author", "", "the name whose comments and folds are replaced, e.g. claude (required)")
	asJSON := fs.Bool("json", false, "emit what was kept, added, removed and skipped as JSON")
	if err := parse(fs, args); err != nil {
		return err
	}
	name := store.Author(*author)
	if *author == "" {
		return usageErrorf("replace needs --author")
	}
	if !name.Valid() {
		return usageErrorf("--author %q is not a name", *author)
	}

	decoder := json.NewDecoder(c.Stdin)
	decoder.DisallowUnknownFields()
	var in replaceInput
	if err := decoder.Decode(&in); err != nil {
		return usageErrorf(`replace reads {"comments": [...], "folds": [...]} on stdin: %v`, err)
	}

	a, s, err := c.openSession(ctx)
	if err != nil {
		return err
	}
	got, err := a.ReplaceNotes(ctx, s, name, in.toNotes())
	if err != nil {
		return err
	}

	if *asJSON {
		if err := writeJSON(c.Stdout, replacedToJSON(got)); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(c.Stdout, "comments: %s\nfolds: %s\n", describeChange(got.Comments), describeChange(got.Folds))
	}
	if len(got.Skipped) == 0 {
		return nil
	}
	reasons := make([]string, 0, len(got.Skipped))
	for _, skipped := range got.Skipped {
		reasons = append(reasons, skipped.Location+": "+skipped.Err.Error())
	}
	return fmt.Errorf("left out %s: %s", plural(len(got.Skipped), "note"), strings.Join(reasons, "; "))
}

func (in replaceInput) toNotes() app.Notes {
	notes := app.Notes{
		Comments: make([]store.Comment, 0, len(in.Comments)),
		Folds:    make([]store.HiddenRun, 0, len(in.Folds)),
	}
	for _, c := range in.Comments {
		notes.Comments = append(notes.Comments, store.Comment{
			File:    c.File,
			Line:    c.Line,
			EndLine: c.EndLine,
			Side:    store.Side(c.Side),
			Origin:  store.Origin(c.Origin),
			Body:    strings.TrimSpace(c.Body),
		})
	}
	for _, f := range in.Folds {
		notes.Folds = append(notes.Folds, store.HiddenRun{
			File:    path.Clean(f.File),
			Line:    f.Line,
			EndLine: f.EndLine,
			Label:   strings.Join(strings.Fields(f.Label), " "),
		})
	}
	return notes
}

func describeChange(c app.Change) string {
	return fmt.Sprintf("%d kept, %d added, %d removed", c.Kept, c.Added, c.Removed)
}

type changeJSON struct {
	Kept    int `json:"kept"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

type skippedJSON struct {
	Location string `json:"location"`
	Error    string `json:"error"`
}

type replacedJSON struct {
	Comments changeJSON    `json:"comments"`
	Folds    changeJSON    `json:"folds"`
	Skipped  []skippedJSON `json:"skipped"`
}

func replacedToJSON(r app.Replaced) replacedJSON {
	skipped := make([]skippedJSON, 0, len(r.Skipped))
	for _, s := range r.Skipped {
		skipped = append(skipped, skippedJSON{Location: s.Location, Error: s.Err.Error()})
	}
	return replacedJSON{
		Comments: changeJSON(r.Comments),
		Folds:    changeJSON(r.Folds),
		Skipped:  skipped,
	}
}
