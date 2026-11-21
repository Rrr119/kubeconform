package validator

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yannh/kubeconform/pkg/resource"
)

const incrementalTestResource = `apiVersion: v1
kind: name
metadata:
  name: example
value: 1
extra: true
`

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
}

func flushValidationCache(t *testing.T, val Validator) {
	t.Helper()
	if err := val.(*v).Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalValidationCacheUsesVersionAndStrictMode(t *testing.T) {
	dir := t.TempDir()
	schemaLocation := filepath.Join(dir, "{{ .NormalizedKubernetesVersion }}{{ .StrictSuffix }}", "{{ .ResourceKind }}.json")
	cacheFile := filepath.Join(dir, "validation-cache.json")

	schema := []byte(`{
  "type": "object",
  "properties": {
    "apiVersion": {"type": "string"},
    "kind": {"type": "string"},
    "metadata": {"type": "object"},
    "value": {"type": "integer"},
    "extra": {"type": "boolean"}
  }
}`)
	strictSchema := []byte(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "apiVersion": {"type": "string"},
    "kind": {"type": "string"},
    "metadata": {"type": "object"},
    "value": {"type": "integer"}
  }
}`)
	v128Schema := []byte(`{
  "type": "object",
  "properties": {
    "apiVersion": {"type": "string"},
    "kind": {"type": "string"},
    "metadata": {"type": "object"},
    "value": {"type": "string"},
    "extra": {"type": "boolean"}
  }
}`)
	writeFile(t, filepath.Join(dir, "v1.27.2", "name.json"), schema)
	writeFile(t, filepath.Join(dir, "v1.27.2-strict", "name.json"), strictSchema)
	writeFile(t, filepath.Join(dir, "v1.28.0", "name.json"), v128Schema)

	validate := func(k8sVersion string, strict bool) Result {
		val, err := New([]string{schemaLocation}, Opts{
			KubernetesVersion: k8sVersion,
			Strict:            strict,
			Incremental:       true,
			IncrementalCache:  cacheFile,
		})
		if err != nil {
			t.Fatal(err)
		}
		return val.ValidateResource(resource.Resource{Path: "manifest.yaml", Bytes: []byte(incrementalTestResource)})
	}

	first := validate("1.27.2", false)
	if first.Status != Valid {
		t.Fatalf("expected valid resource, got %d: %v", first.Status, first.Err)
	}
	val, err := New([]string{schemaLocation}, Opts{
		KubernetesVersion: "1.27.2",
		Incremental:       true,
		IncrementalCache:  cacheFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result := val.ValidateResource(resource.Resource{Bytes: []byte(incrementalTestResource)}); result.Status != Valid {
		t.Fatalf("expected cached valid resource, got %d: %v", result.Status, result.Err)
	}
	flushValidationCache(t, val)

	if result := validate("1.28.0", false); result.Status != Invalid {
		t.Fatalf("expected a different Kubernetes version to miss the cache, got %d: %v", result.Status, result.Err)
	}
	if result := validate("1.27.2", true); result.Status != Invalid {
		t.Fatalf("expected strict mode to miss the cache and fail validation, got %d: %v", result.Status, result.Err)
	}
}

func TestIncrementalValidationCacheInvalidatesChangedResources(t *testing.T) {
	dir := t.TempDir()
	schemaFile := filepath.Join(dir, "name.json")
	schemaLocation := filepath.Join(dir, "{{ .ResourceKind }}.json")
	cacheFile := filepath.Join(dir, "validation-cache.json")
	writeFile(t, schemaFile, []byte(`{
  "type": "object",
  "properties": {
    "apiVersion": {"type": "string"},
    "kind": {"type": "string"},
    "metadata": {"type": "object"},
    "value": {"type": "integer"}
  }
}`))

	opts := Opts{
		KubernetesVersion: "1.27.2",
		Incremental:       true,
		IncrementalCache:  cacheFile,
	}
	val, err := New([]string{schemaLocation}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result := val.ValidateResource(resource.Resource{Bytes: []byte(incrementalTestResource)}); result.Status != Valid {
		t.Fatalf("expected valid resource, got %d: %v", result.Status, result.Err)
	}
	flushValidationCache(t, val)

	if err := os.Remove(schemaFile); err != nil {
		t.Fatal(err)
	}
	val, err = New([]string{schemaLocation}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result := val.ValidateResource(resource.Resource{Bytes: []byte(incrementalTestResource)}); result.Status != Valid {
		t.Fatalf("expected unchanged resource to be loaded from cache, got %d: %v", result.Status, result.Err)
	}

	changed := bytes.Replace([]byte(incrementalTestResource), []byte("value: 1"), []byte("value: 2"), 1)
	if result := val.ValidateResource(resource.Resource{Bytes: changed}); result.Status != Error {
		t.Fatalf("expected changed resource to be validated again, got %d: %v", result.Status, result.Err)
	}
}

func TestIncrementalValidationCacheCachesInvalidResources(t *testing.T) {
	dir := t.TempDir()
	schemaFile := filepath.Join(dir, "name.json")
	schemaLocation := filepath.Join(dir, "{{ .ResourceKind }}.json")
	cacheFile := filepath.Join(dir, "validation-cache.json")
	writeFile(t, schemaFile, []byte(`{
  "type": "object",
  "properties": {
    "apiVersion": {"type": "string"},
    "kind": {"type": "string"},
    "metadata": {"type": "object"},
    "value": {"type": "integer", "minimum": 10}
  }
}`))

	opts := Opts{
		KubernetesVersion: "1.27.2",
		Incremental:       true,
		IncrementalCache:  cacheFile,
	}
	val, err := New([]string{schemaLocation}, opts)
	if err != nil {
		t.Fatal(err)
	}
	first := val.ValidateResource(resource.Resource{Bytes: []byte(incrementalTestResource)})
	if first.Status != Invalid || len(first.ValidationErrors) == 0 {
		t.Fatalf("expected invalid resource, got %d: %v", first.Status, first.Err)
	}
	flushValidationCache(t, val)

	if err := os.Remove(schemaFile); err != nil {
		t.Fatal(err)
	}
	val, err = New([]string{schemaLocation}, opts)
	if err != nil {
		t.Fatal(err)
	}
	second := val.ValidateResource(resource.Resource{Bytes: []byte(incrementalTestResource)})
	if second.Status != Invalid || second.Err == nil || len(second.ValidationErrors) == 0 {
		t.Fatalf("expected cached invalid result, got %d: %v", second.Status, second.Err)
	}
}

func TestValidate1000ManifestsUnderTenSeconds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}

	dir := t.TempDir()
	schemaLocation := filepath.Join(dir, "schemas", "{{ .ResourceKind }}.json")
	writeFile(t, filepath.Join(dir, "schemas", "name.json"), []byte(`{
  "type": "object",
  "properties": {
    "apiVersion": {"type": "string"},
    "kind": {"type": "string"},
    "metadata": {"type": "object"},
    "value": {"type": "integer"}
  }
}`))

	manifests := filepath.Join(dir, "manifests")
	for i := 0; i < 1000; i++ {
		writeFile(t, filepath.Join(manifests, fmt.Sprintf("manifest-%04d.yaml", i)), []byte(fmt.Sprintf(`apiVersion: v1
kind: name
metadata:
  name: manifest-%04d
value: 1
`, i)))
	}

	start := time.Now()
	val, err := New([]string{schemaLocation}, Opts{KubernetesVersion: "1.27.2"})
	if err != nil {
		t.Fatal(err)
	}

	resources, discoveryErrors := resource.FromFiles(context.Background(), []string{manifests}, nil, 4)
	errorDone := make(chan struct{})
	go func() {
		defer close(errorDone)
		for err := range discoveryErrors {
			if err != nil {
				t.Errorf("resource discovery failed: %v", err)
			}
		}
	}()

	var valid, invalid, failed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for res := range resources {
				switch val.ValidateResource(res).Status {
				case Valid:
					valid.Add(1)
				case Invalid:
					invalid.Add(1)
				default:
					failed.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	<-errorDone

	elapsed := time.Since(start)
	if valid.Load() != 1000 || invalid.Load() != 0 || failed.Load() != 0 {
		t.Fatalf("expected 1000 valid manifests, got valid=%d invalid=%d failed=%d", valid.Load(), invalid.Load(), failed.Load())
	}
	if elapsed > 10*time.Second {
		t.Fatalf("validated 1000 manifests in %s, expected at most 10s", elapsed)
	}
	t.Logf("validated 1000 manifests in %s", elapsed)
}
