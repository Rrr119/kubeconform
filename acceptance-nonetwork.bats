#!/usr/bin/env bats

@test "Fail when parsing a valid Kubernetes config YAML file without network access" {
  run bin/kubeconform fixtures/valid.yaml
  [ "$status" -eq 1 ]
}

@test "Pass when parsing a valid config YAML file without network access, with cache" {
  run bin/kubeconform -cache fixtures/cache/ fixtures/valid.yaml
  [ "$status" -eq 0 ]
}

@test "Pass when parsing a Custom Resource and using a local schema registry with appropriate CRD" {
  run bin/kubeconform -schema-location './fixtures/registry/{{ .ResourceKind }}{{ .KindSuffix }}.json' fixtures/test_crd.yaml
  [ "$status" -eq 0 ]
}

@test "Pass when parsing a Custom Resource and specifying several local registries, the last one having the appropriate CRD" {
  run bin/kubeconform -schema-location 'fixtures/{{ .ResourceKind }}.json' -schema-location './fixtures/registry/{{ .ResourceKind }}{{ .KindSuffix }}.json' fixtures/test_crd.yaml
  [ "$status" -eq 0 ]
}

@test "Pass when using a cached schema with external references" {
  run bin/kubeconform -cache fixtures/cache -summary -schema-location 'https://raw.githubusercontent.com/yannh/kubernetes-json-schema/master/{{ .NormalizedKubernetesVersion }}{{ .StrictSuffix }}/{{ .ResourceKind }}{{ .KindSuffix }}.json' fixtures/valid.yaml
  [ "$status" -eq 0 ]
}

@test "Reuse unchanged validation results with incremental validation" {
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' EXIT
  cp fixtures/test_crd.yaml "$tmpdir/manifest.yaml"
  cp fixtures/registry/trainingjob-sagemaker-v1.json "$tmpdir/trainingjob-sagemaker-v1.json"

  schema_location="$tmpdir/{{ .ResourceKind }}{{ .KindSuffix }}.json"
  run bin/kubeconform -incremental -incremental-cache "$tmpdir/validation-cache.json" -schema-location "$schema_location" "$tmpdir/manifest.yaml"
  [ "$status" -eq 0 ]
  [ -f "$tmpdir/validation-cache.json" ]

  rm "$tmpdir/trainingjob-sagemaker-v1.json"
  run bin/kubeconform -incremental -incremental-cache "$tmpdir/validation-cache.json" -schema-location "$schema_location" "$tmpdir/manifest.yaml"
  [ "$status" -eq 0 ]

  run bin/kubeconform -incremental -incremental-cache "$tmpdir/validation-cache.json" -kubernetes-version 1.20.0 -schema-location "$schema_location" "$tmpdir/manifest.yaml"
  [ "$status" -eq 1 ]
}
