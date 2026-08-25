// Tests for hf_mirror.go: path traversal 防御。
package proxy

import "testing"

func TestHasPathTraversal_NoTraversal(t *testing.T) {
	cases := []string{
		"api/models/Qwen/Qwen2.5-7B-Instruct/tree/main",
		"Qwen/Qwen2.5-1.5B-Instruct/resolve/main/config.json",
		"Qwen/Qwen2.5/resolve/main/sub/dir/file.txt",
		"",
	}
	for _, c := range cases {
		if hasPathTraversal(c) {
			t.Errorf("hasPathTraversal(%q) = true, want false", c)
		}
	}
}

func TestHasPathTraversal_TraversalDetected(t *testing.T) {
	cases := []string{
		"Qwen/Qwen/resolve/main/../../etc/passwd",
		"api/../models/Qwen/Qwen2.5/tree",
		"foo/../bar",
		"foo/..",
		"../etc/passwd",
		"Qwen/..",
	}
	for _, c := range cases {
		if !hasPathTraversal(c) {
			t.Errorf("hasPathTraversal(%q) = false, want true", c)
		}
	}
}
