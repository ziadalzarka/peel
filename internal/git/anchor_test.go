package git

import "testing"

// TestLineMapMovesLinesPastEveryChangedRange checks the arithmetic the whole
// anchor rests on: a diff read as "where did line N go".
func TestLineMapMovesLinesPastEveryChangedRange(t *testing.T) {
	tests := []struct {
		name string
		diff string
		line int
		want int
		gone bool
	}{
		{
			name: "an insertion above pushes the line down",
			diff: "@@ -2,0 +3,2 @@\n+import \"context\"\n+\n",
			line: 4, want: 6,
		},
		{
			name: "the line an insertion follows keeps its number",
			diff: "@@ -2,0 +3,2 @@\n+a\n+b\n",
			line: 2, want: 2,
		},
		{
			name: "an insertion below leaves the line alone",
			diff: "@@ -9,0 +10,1 @@\n+trailing\n",
			line: 4, want: 4,
		},
		{
			name: "a deletion above pulls the line up",
			diff: "@@ -2,2 +1,0 @@\n-gone\n-also gone\n",
			line: 6, want: 4,
		},
		{
			name: "the line itself being rewritten has nowhere to go",
			diff: "@@ -4,1 +4,1 @@\n-\tdoWork()\n+\tdoNothing()\n",
			line: 4, gone: true,
		},
		{
			name: "the line itself being deleted has nowhere to go",
			diff: "@@ -4,1 +3,0 @@\n-\tdoWork()\n",
			line: 4, gone: true,
		},
		{
			name: "several ranges accumulate",
			diff: "@@ -1,0 +2,3 @@\n+a\n+b\n+c\n@@ -5,2 +8,0 @@\n-x\n-y\n",
			line: 9, want: 10,
		},
		{
			name: "an unchanged file moves nothing",
			diff: "",
			line: 42, want: 42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := parseLineMap(tt.diff)
			if err != nil {
				t.Fatalf("parseLineMap: %v", err)
			}
			got, ok := m.Lookup(tt.line)
			if tt.gone {
				if ok {
					t.Fatalf("Lookup(%d) = %d, want the line reported as gone", tt.line, got)
				}
				return
			}
			if !ok {
				t.Fatalf("Lookup(%d) reported gone, want %d", tt.line, tt.want)
			}
			if got != tt.want {
				t.Errorf("Lookup(%d) = %d, want %d", tt.line, got, tt.want)
			}
		})
	}
}

func TestLineMapIdentityOnAnEmptyDiff(t *testing.T) {
	m, err := parseLineMap("")
	if err != nil {
		t.Fatalf("parseLineMap: %v", err)
	}
	if !m.Identity() {
		t.Error("Identity = false on a diff with no changed ranges")
	}
}

func TestLineMapNearestPlacesARewrittenOrDeletedLineBesideWhereItWas(t *testing.T) {
	tests := []struct {
		name string
		diff string
		line int
		want int
	}{
		{
			name: "a line rewritten in place keeps its number",
			diff: "@@ -4,1 +4,1 @@\n-\tdoWork()\n+\tdoNothing()\n",
			line: 4, want: 4,
		},
		{
			name: "a line rewritten into several lands on the first of them",
			diff: "@@ -529 +529,6 @@\n-x\n+a\n+b\n+c\n+d\n+e\n+f\n",
			line: 529, want: 529,
		},
		{
			name: "a line inside a block rewritten into fewer lands on the last of them",
			diff: "@@ -28,5 +28,2 @@\n-a\n-b\n-c\n-d\n-e\n+x\n+y\n",
			line: 32, want: 29,
		},
		{
			name: "a rewritten line moves with the ranges above it",
			diff: "@@ -1,0 +2,5 @@\n+a\n+b\n+c\n+d\n+e\n@@ -532 +537 @@\n-old\n+new\n",
			line: 532, want: 537,
		},
		{
			name: "a deleted line lands on the line before the gap",
			diff: "@@ -4,2 +3,0 @@\n-c\n-d\n",
			line: 5, want: 3,
		},
		{
			name: "a line deleted from the top of the file lands on line 1",
			diff: "@@ -1,2 +0,0 @@\n-a\n-b\n",
			line: 1, want: 1,
		},
		{
			name: "a line that is still there answers where it is",
			diff: "@@ -2,0 +3,2 @@\n+a\n+b\n",
			line: 4, want: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := parseLineMap(tt.diff)
			if err != nil {
				t.Fatalf("parseLineMap: %v", err)
			}
			if got := m.Nearest(tt.line); got != tt.want {
				t.Errorf("Nearest(%d) = %d, want %d", tt.line, got, tt.want)
			}
		})
	}
}
