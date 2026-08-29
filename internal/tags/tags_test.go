package tags

import (
	"reflect"
	"testing"
)

func TestResolveExpand(t *testing.T) {
	t.Parallel()
	got, err := Resolve([]string{"v1.2.3"}, true, false, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1", "1.2", "1.2.3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve() = %#v, want %#v", got, want)
	}
}

func TestResolveExpandPartialVersion(t *testing.T) {
	t.Parallel()
	got, err := Resolve([]string{"v1.2"}, true, false, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1", "1.2", "1.2.0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve() = %#v, want %#v", got, want)
	}
}

func TestResolveExpandPreservesMetadata(t *testing.T) {
	t.Parallel()
	got, err := Resolve([]string{"v1.2.3+build-info"}, true, false, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1+build-info", "1.2+build-info", "1.2.3+build-info"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve() = %#v, want %#v", got, want)
	}
}

func TestResolveExpandPrereleaseIsExact(t *testing.T) {
	t.Parallel()
	got, err := Resolve([]string{"v1.2.3-rc1"}, true, false, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.2.3-rc1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve() = %#v, want %#v", got, want)
	}
}

func TestResolveAutomaticBranch(t *testing.T) {
	t.Parallel()
	got, err := Resolve([]string{"latest"}, false, true, "linux", "refs/heads/main", "main")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"linux"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve() = %#v, want %#v", got, want)
	}
}
