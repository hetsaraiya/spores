package github

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

func (c *Client) GetFileContent(ctx context.Context, full, path, ref string) (string, error) {
	base, err := repoPath(full)
	if err != nil {
		return "", err
	}
	endpoint := base + "/contents/" + url.PathEscape(path)
	if ref != "" {
		endpoint += "?ref=" + url.QueryEscape(ref)
	}
	var file struct {
		Content string `json:"content"`
	}
	if err := c.get(ctx, endpoint, &file); err != nil {
		return "", err
	}
	content, err := decodeContent(file.Content)
	if err != nil {
		return "", err
	}
	return clip(content), nil
}

func (c *Client) ListDir(ctx context.Context, full, path, ref string) (string, error) {
	base, err := repoPath(full)
	if err != nil {
		return "", err
	}
	endpoint := base + "/contents"
	if path != "" {
		endpoint += "/" + url.PathEscape(path)
	}
	if ref != "" {
		endpoint += "?ref=" + url.QueryEscape(ref)
	}
	var entries []struct {
		Type string `json:"type"`
		Path string `json:"path"`
	}
	if err := c.get(ctx, endpoint, &entries); err != nil {
		return "", err
	}
	var out strings.Builder
	for _, entry := range entries {
		fmt.Fprintf(&out, "%s\t%s\n", entry.Type, entry.Path)
	}
	return clip(out.String()), nil
}

func (c *Client) GetTree(ctx context.Context, full, ref string) (string, error) {
	base, err := repoPath(full)
	if err != nil {
		return "", err
	}
	if ref == "" {
		ref = "HEAD"
	}
	var tree struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Type string `json:"type"`
			Path string `json:"path"`
		} `json:"tree"`
	}
	if err := c.get(ctx, base+"/git/trees/"+url.PathEscape(ref)+"?recursive=1", &tree); err != nil {
		return "", err
	}
	var out strings.Builder
	for _, entry := range tree.Tree {
		fmt.Fprintf(&out, "%s\t%s\n", entry.Type, entry.Path)
	}
	if tree.Truncated {
		out.WriteString("... [tree truncated by GitHub]\n")
	}
	return clip(out.String()), nil
}

func (c *Client) ListBranches(ctx context.Context, full string) (string, error) {
	base, err := repoPath(full)
	if err != nil {
		return "", err
	}
	var branches []struct {
		Name string `json:"name"`
	}
	if err := c.get(ctx, base+"/branches?per_page=100", &branches); err != nil {
		return "", err
	}
	var out strings.Builder
	for _, branch := range branches {
		fmt.Fprintln(&out, branch.Name)
	}
	return clip(out.String()), nil
}
