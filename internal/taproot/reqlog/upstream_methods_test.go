package reqlog

import (
	"reflect"
	"strings"
	"testing"

	"github.com/265866/taproot/internal/logging"
)

// TestMethodsMatchUpstreamLogger holds taproot's logger and stream writer to
// the exported methods of upstream's own implementations. Upstream finds some
// of them by asserting anonymous interfaces, such as interface{ SetEnabled(bool) }
// in internal/api and WriteAPIRequestSource in its middleware, which no
// compile-time assertion can name. Without this test, a method upstream adds
// or changes in a sync would make those features fall back silently.
func TestMethodsMatchUpstreamLogger(t *testing.T) {
	cases := []struct {
		upstream, taproot reflect.Type
		// skip names upstream methods taproot leaves out on purpose.
		skip map[string]string
	}{
		{
			upstream: reflect.TypeFor[*logging.FileRequestLogger](),
			taproot:  reflect.TypeFor[*requestLogger](),
			skip:     map[string]string{"SetHomeEnabled": "taproot refuses to start in Home mode"},
		},
		{
			upstream: reflect.TypeFor[*logging.FileStreamingLogWriter](),
			taproot:  reflect.TypeFor[*streamWriter](),
		},
	}
	for _, tc := range cases {
		if tc.upstream.NumMethod() == 0 {
			t.Fatalf("%s has no exported methods", tc.upstream)
		}
		for i := range tc.upstream.NumMethod() {
			want := tc.upstream.Method(i)
			if _, ok := tc.skip[want.Name]; ok {
				continue
			}
			got, ok := tc.taproot.MethodByName(want.Name)
			if !ok {
				t.Errorf("%s has no method %s%s, which upstream's %s has", tc.taproot, want.Name, signature(want.Type), tc.upstream)
				continue
			}
			if signature(got.Type) != signature(want.Type) {
				t.Errorf("%s.%s is %s; upstream's %s has %s", tc.taproot, want.Name, signature(got.Type), tc.upstream, signature(want.Type))
			}
		}
	}
}

// signature formats a method type without its receiver.
func signature(fn reflect.Type) string {
	in := make([]string, 0, fn.NumIn())
	for i := 1; i < fn.NumIn(); i++ {
		in = append(in, fn.In(i).String())
	}
	out := make([]string, 0, fn.NumOut())
	for i := range fn.NumOut() {
		out = append(out, fn.Out(i).String())
	}
	return "(" + strings.Join(in, ", ") + ") (" + strings.Join(out, ", ") + ")"
}
