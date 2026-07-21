package memos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type memoRelationsPage struct {
	Relations     []json.RawMessage `json:"relations"`
	NextPageToken string            `json:"nextPageToken"`
}

// AddMemoReference preserves existing relations and adds a REFERENCE relation.
func (c *Client) AddMemoReference(ctx context.Context, memoUID, relatedMemoUID string) (json.RawMessage, error) {
	memoName := resourceName("memos", memoUID)
	relatedMemoName := resourceName("memos", relatedMemoUID)

	relations, err := c.listMemoRelations(ctx, memoUID)
	if err != nil {
		return nil, err
	}
	for _, relation := range relations {
		if isMemoReference(relation, relatedMemoName) {
			return json.RawMessage(`{}`), nil
		}
	}

	relations = append(relations, newMemoReference(memoName, relatedMemoName))
	return c.Do(ctx, http.MethodPatch, memoRelationsPath(memoUID), map[string]any{
		"name":      memoName,
		"relations": relations,
	})
}

func (c *Client) listMemoRelations(ctx context.Context, memoUID string) ([]json.RawMessage, error) {
	var relations []json.RawMessage
	pageToken := ""
	for {
		path := memoRelationsPath(memoUID)
		if pageToken != "" {
			query := url.Values{}
			query.Set("pageToken", pageToken)
			path += "?" + query.Encode()
		}
		raw, err := c.Do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var page memoRelationsPage
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("decode memo relations: %w", err)
		}
		relations = append(relations, page.Relations...)
		if page.NextPageToken == "" {
			return relations, nil
		}
		if page.NextPageToken == pageToken {
			return nil, fmt.Errorf("list memo relations returned repeated page token %q", pageToken)
		}
		pageToken = page.NextPageToken
	}
}

func isMemoReference(raw json.RawMessage, relatedMemoName string) bool {
	var relation struct {
		RelatedMemo json.RawMessage `json:"relatedMemo"`
		Type        string          `json:"type"`
	}
	if json.Unmarshal(raw, &relation) != nil || relation.Type != "REFERENCE" {
		return false
	}
	return memoName(relation.RelatedMemo) == relatedMemoName
}

func memoName(raw json.RawMessage) string {
	var name string
	if json.Unmarshal(raw, &name) == nil {
		return name
	}
	var resource struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(raw, &resource)
	return resource.Name
}

func newMemoReference(memoName, relatedMemoName string) json.RawMessage {
	relation := map[string]any{
		"memo":        map[string]string{"name": memoName},
		"relatedMemo": map[string]string{"name": relatedMemoName},
		"type":        "REFERENCE",
	}
	raw, _ := json.Marshal(relation)
	return raw
}

func memoRelationsPath(memoUID string) string {
	return "/api/v1/memos/" + url.PathEscape(strings.TrimPrefix(memoUID, "memos/")) + "/relations"
}

func resourceName(resource, id string) string {
	if strings.HasPrefix(id, resource+"/") {
		return id
	}
	return resource + "/" + id
}
