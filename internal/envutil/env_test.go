package envutil

import (
	"reflect"
	"testing"
)

func TestCSV(t *testing.T) {
	t.Parallel()
	want := []string{"one", "two", "three"}
	if got := CSV(" one, two ,,three "); !reflect.DeepEqual(got, want) {
		t.Fatalf("CSV() = %#v, want %#v", got, want)
	}
}

func TestSemicolonPreservesCommas(t *testing.T) {
	t.Parallel()
	want := []string{"type=registry,ref=example/cache", "type=inline"}
	if got := Semicolon("type=registry,ref=example/cache;type=inline"); !reflect.DeepEqual(got, want) {
		t.Fatalf("Semicolon() = %#v, want %#v", got, want)
	}
}
