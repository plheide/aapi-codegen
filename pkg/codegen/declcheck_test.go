package codegen

import (
	"strings"
	"testing"
)

func TestCheckDuplicateDeclarations(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantDup []string // names the error must list; empty means no error
	}{
		{
			name: "clean",
			src: `package x
type T struct{}
type U struct{}
func (t T) M() {}
func (u *U) M() {}
func F() {}
func init() {}
func init() {}
var _ = 1
var _ = 2`,
		},
		{
			name: "duplicate type",
			src: `package x
type Handler interface{}
type Handler interface{}`,
			wantDup: []string{"Handler"},
		},
		{
			name: "duplicate method across value and pointer receivers",
			src: `package x
type S struct{}
func (s S) Subscribe() {}
func (s *S) Subscribe() {}`,
			wantDup: []string{"S.Subscribe"},
		},
		{
			name: "type and func share a name",
			src: `package x
type New struct{}
func New() {}`,
			wantDup: []string{"New"},
		},
		{
			name: "duplicate var",
			src: `package x
var ErrDrop error
var ErrDrop error`,
			wantDup: []string{"ErrDrop"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkDuplicateDeclarations([]byte(tt.src))
			if len(tt.wantDup) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("no error; want duplicates %v", tt.wantDup)
			}
			for _, name := range tt.wantDup {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("error %q does not name %q", err, name)
				}
			}
		})
	}
}
