package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockCanvasClient implements provider.SlackAPI for canvas handler tests. It
// embeds the interface so all non-canvas methods are satisfied automatically,
// and records the parameters passed to each canvas call.
type mockCanvasClient struct {
	provider.SlackAPI

	createTitle string
	createDoc   slack.DocumentContent
	createID    string
	createErr   error

	channelCreateChannelID string
	channelCreateDoc       slack.DocumentContent
	channelCreateOptCount  int
	channelCreateID        string
	channelCreateErr       error

	editParams slack.EditCanvasParams
	editErr    error

	deleteID  string
	deleteErr error

	lookupParams   slack.LookupCanvasSectionsParams
	lookupSections []slack.CanvasSection
	lookupErr      error

	accessSetParams slack.SetCanvasAccessParams
	accessSetErr    error

	accessDeleteParams slack.DeleteCanvasAccessParams
	accessDeleteErr    error
}

func (m *mockCanvasClient) CreateCanvasContext(_ context.Context, title string, doc slack.DocumentContent) (string, error) {
	m.createTitle = title
	m.createDoc = doc
	return m.createID, m.createErr
}

func (m *mockCanvasClient) CreateChannelCanvasContext(_ context.Context, channelID string, doc slack.DocumentContent, options ...slack.CreateChannelCanvasOption) (string, error) {
	m.channelCreateChannelID = channelID
	m.channelCreateDoc = doc
	m.channelCreateOptCount = len(options)
	return m.channelCreateID, m.channelCreateErr
}

func (m *mockCanvasClient) EditCanvasContext(_ context.Context, params slack.EditCanvasParams) error {
	m.editParams = params
	return m.editErr
}

func (m *mockCanvasClient) DeleteCanvasContext(_ context.Context, canvasID string) error {
	m.deleteID = canvasID
	return m.deleteErr
}

func (m *mockCanvasClient) LookupCanvasSectionsContext(_ context.Context, params slack.LookupCanvasSectionsParams) ([]slack.CanvasSection, error) {
	m.lookupParams = params
	return m.lookupSections, m.lookupErr
}

func (m *mockCanvasClient) SetCanvasAccessContext(_ context.Context, params slack.SetCanvasAccessParams) error {
	m.accessSetParams = params
	return m.accessSetErr
}

func (m *mockCanvasClient) DeleteCanvasAccessContext(_ context.Context, params slack.DeleteCanvasAccessParams) error {
	m.accessDeleteParams = params
	return m.accessDeleteErr
}

func newCanvasTestHandler(client *mockCanvasClient) *CanvasHandler {
	ap := provider.NewTestProvider(client, zap.NewNop())
	return NewCanvasHandler(ap, zap.NewNop())
}

func canvasRequest(args map[string]any) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.Arguments = args
	return req
}

// resultText extracts the text payload from a successful tool result.
func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.NotNil(t, res)
	require.Len(t, res.Content, 1)
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok, "content should be TextContent")
	return tc.Text
}

// resultJSON extracts and unmarshals the JSON payload from a successful result.
func resultJSON(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &payload))
	return payload
}

// --- buildCanvasChange (pure validation) -----------------------------------

func TestUnitBuildCanvasChange(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		sectionID string
		markdown  string
		wantErr   string // empty == expect success
	}{
		{name: "insert_at_start ok", operation: "insert_at_start", markdown: "hi"},
		{name: "insert_at_end ok", operation: "insert_at_end", markdown: "hi"},
		{name: "insert_after with section ok", operation: "insert_after", sectionID: "s1", markdown: "hi"},
		{name: "insert_before with section ok", operation: "insert_before", sectionID: "s1", markdown: "hi"},
		{name: "replace with section ok", operation: "replace", sectionID: "s1", markdown: "hi"},
		{name: "delete with section ok", operation: "delete", sectionID: "s1"},
		{name: "empty operation", operation: "", markdown: "hi", wantErr: "operation is required"},
		{name: "invalid operation", operation: "frobnicate", markdown: "hi", wantErr: "invalid operation"},
		{name: "insert_after missing section", operation: "insert_after", markdown: "hi", wantErr: "section_id is required"},
		{name: "insert_before missing section", operation: "insert_before", markdown: "hi", wantErr: "section_id is required"},
		{name: "replace missing section", operation: "replace", markdown: "hi", wantErr: "section_id is required"},
		{name: "delete missing section", operation: "delete", wantErr: "section_id is required"},
		{name: "insert_at_end missing markdown", operation: "insert_at_end", wantErr: "markdown is required"},
		{name: "replace missing markdown", operation: "replace", sectionID: "s1", wantErr: "markdown is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			change, err := buildCanvasChange(tt.operation, tt.sectionID, tt.markdown)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.operation, change.Operation)
			assert.Equal(t, tt.sectionID, change.SectionID)
			if tt.operation == "delete" {
				// delete carries no content
				assert.Equal(t, slack.DocumentContent{}, change.DocumentContent)
			} else {
				assert.Equal(t, "markdown", change.DocumentContent.Type)
				assert.Equal(t, tt.markdown, change.DocumentContent.Markdown)
			}
		})
	}
}

// --- canvas_create ----------------------------------------------------------

func TestUnitCanvasCreateHandler(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := &mockCanvasClient{createID: "F123"}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasCreateHandler(context.Background(), canvasRequest(map[string]any{
			"title":    "My Canvas",
			"markdown": "# Hello",
		}))
		require.NoError(t, err)

		payload := resultJSON(t, res)
		assert.Equal(t, "F123", payload["canvas_id"])
		assert.Equal(t, "created", payload["status"])
		assert.Equal(t, "My Canvas", m.createTitle)
		assert.Equal(t, "markdown", m.createDoc.Type)
		assert.Equal(t, "# Hello", m.createDoc.Markdown)
	})

	t.Run("missing markdown", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasCreateHandler(context.Background(), canvasRequest(map[string]any{
			"title": "My Canvas",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "markdown is required")
	})

	t.Run("slack error propagates", func(t *testing.T) {
		m := &mockCanvasClient{createErr: errors.New("canvas_creation_failed")}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasCreateHandler(context.Background(), canvasRequest(map[string]any{
			"markdown": "# Hello",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "canvas_creation_failed")
	})
}

// --- channel_canvas_create --------------------------------------------------

func TestUnitCanvasChannelCreateHandler(t *testing.T) {
	t.Run("success with title option", func(t *testing.T) {
		m := &mockCanvasClient{channelCreateID: "F999"}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasChannelCreateHandler(context.Background(), canvasRequest(map[string]any{
			"channel_id": "C123",
			"title":      "Channel Canvas",
			"markdown":   "body",
		}))
		require.NoError(t, err)

		payload := resultJSON(t, res)
		assert.Equal(t, "F999", payload["canvas_id"])
		assert.Equal(t, "C123", payload["channel_id"])
		assert.Equal(t, "C123", m.channelCreateChannelID)
		assert.Equal(t, "body", m.channelCreateDoc.Markdown)
		assert.Equal(t, 1, m.channelCreateOptCount, "title option should be passed")
	})

	t.Run("success without title option", func(t *testing.T) {
		m := &mockCanvasClient{channelCreateID: "F999"}
		h := newCanvasTestHandler(m)

		_, err := h.CanvasChannelCreateHandler(context.Background(), canvasRequest(map[string]any{
			"channel_id": "C123",
			"markdown":   "body",
		}))
		require.NoError(t, err)
		assert.Equal(t, 0, m.channelCreateOptCount, "no title option should be passed")
	})

	t.Run("missing channel_id", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasChannelCreateHandler(context.Background(), canvasRequest(map[string]any{
			"markdown": "body",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "channel_id is required")
	})

	t.Run("missing markdown", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasChannelCreateHandler(context.Background(), canvasRequest(map[string]any{
			"channel_id": "C123",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "markdown is required")
	})
}

// --- canvas_edit ------------------------------------------------------------

func TestUnitCanvasEditHandler(t *testing.T) {
	t.Run("insert_at_end success", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasEditHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id": "F123",
			"operation": "insert_at_end",
			"markdown":  "new content",
		}))
		require.NoError(t, err)

		payload := resultJSON(t, res)
		assert.Equal(t, "F123", payload["canvas_id"])
		assert.Equal(t, "edited", payload["status"])

		require.Equal(t, "F123", m.editParams.CanvasID)
		require.Len(t, m.editParams.Changes, 1)
		assert.Equal(t, "insert_at_end", m.editParams.Changes[0].Operation)
		assert.Equal(t, "new content", m.editParams.Changes[0].DocumentContent.Markdown)
	})

	t.Run("replace with section_id success", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		_, err := h.CanvasEditHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id":  "F123",
			"operation":  "replace",
			"section_id": "sec-1",
			"markdown":   "updated",
		}))
		require.NoError(t, err)
		require.Len(t, m.editParams.Changes, 1)
		assert.Equal(t, "replace", m.editParams.Changes[0].Operation)
		assert.Equal(t, "sec-1", m.editParams.Changes[0].SectionID)
	})

	t.Run("delete with section_id carries no content", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		_, err := h.CanvasEditHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id":  "F123",
			"operation":  "delete",
			"section_id": "sec-1",
		}))
		require.NoError(t, err)
		require.Len(t, m.editParams.Changes, 1)
		assert.Equal(t, "delete", m.editParams.Changes[0].Operation)
		assert.Equal(t, slack.DocumentContent{}, m.editParams.Changes[0].DocumentContent)
	})

	t.Run("missing section_id for replace", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasEditHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id": "F123",
			"operation": "replace",
			"markdown":  "x",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "section_id is required")
	})

	t.Run("missing section_id for delete", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasEditHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id": "F123",
			"operation": "delete",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "section_id is required")
	})

	t.Run("invalid operation", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasEditHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id": "F123",
			"operation": "nope",
			"markdown":  "x",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "invalid operation")
	})

	t.Run("missing canvas_id", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasEditHandler(context.Background(), canvasRequest(map[string]any{
			"operation": "insert_at_end",
			"markdown":  "x",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "canvas_id is required")
	})

	t.Run("slack error propagates", func(t *testing.T) {
		m := &mockCanvasClient{editErr: errors.New("section_not_found")}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasEditHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id": "F123",
			"operation": "insert_at_end",
			"markdown":  "x",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "section_not_found")
	})
}

// --- canvas_delete ----------------------------------------------------------

func TestUnitCanvasDeleteHandler(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasDeleteHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id": "F123",
		}))
		require.NoError(t, err)

		payload := resultJSON(t, res)
		assert.Equal(t, "F123", payload["canvas_id"])
		assert.Equal(t, "deleted", payload["status"])
		assert.Equal(t, "F123", m.deleteID)
	})

	t.Run("missing canvas_id", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasDeleteHandler(context.Background(), canvasRequest(map[string]any{}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "canvas_id is required")
	})

	t.Run("slack error propagates", func(t *testing.T) {
		m := &mockCanvasClient{deleteErr: errors.New("canvas_not_found")}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasDeleteHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id": "F404",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "canvas_not_found")
	})
}

// --- canvas_sections_lookup -------------------------------------------------

func TestUnitCanvasSectionsLookupHandler(t *testing.T) {
	t.Run("success returns section ids", func(t *testing.T) {
		m := &mockCanvasClient{lookupSections: []slack.CanvasSection{{ID: "s1"}, {ID: "s2"}}}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasSectionsLookupHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id":     "F123",
			"section_types": "h1,h2",
			"contains_text": "Goals",
		}))
		require.NoError(t, err)

		payload := resultJSON(t, res)
		assert.Equal(t, "F123", payload["canvas_id"])
		assert.Equal(t, float64(2), payload["count"])
		assert.Equal(t, []any{"s1", "s2"}, payload["sections"])

		assert.Equal(t, []string{"h1", "h2"}, m.lookupParams.Criteria.SectionTypes)
		assert.Equal(t, "Goals", m.lookupParams.Criteria.ContainsText)
	})

	t.Run("contains_text only is valid", func(t *testing.T) {
		m := &mockCanvasClient{lookupSections: []slack.CanvasSection{}}
		h := newCanvasTestHandler(m)

		_, err := h.CanvasSectionsLookupHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id":     "F123",
			"contains_text": "x",
		}))
		require.NoError(t, err)
	})

	t.Run("invalid section type", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasSectionsLookupHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id":     "F123",
			"section_types": "h1,h7",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "invalid section type")
	})

	t.Run("no criteria", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasSectionsLookupHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id": "F123",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "at least one of section_types or contains_text")
	})

	t.Run("missing canvas_id", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasSectionsLookupHandler(context.Background(), canvasRequest(map[string]any{
			"contains_text": "x",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "canvas_id is required")
	})

	t.Run("slack error propagates", func(t *testing.T) {
		m := &mockCanvasClient{lookupErr: errors.New("canvas_not_found")}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasSectionsLookupHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id":     "F404",
			"contains_text": "x",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "canvas_not_found")
	})
}

// --- canvas_access_set ------------------------------------------------------

func TestUnitCanvasAccessSetHandler(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasAccessSetHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id":    "F123",
			"access_level": "write",
			"channel_ids":  "C1, C2",
			"user_ids":     "U1",
		}))
		require.NoError(t, err)

		payload := resultJSON(t, res)
		assert.Equal(t, "F123", payload["canvas_id"])
		assert.Equal(t, "write", payload["access_level"])
		assert.Equal(t, "write", m.accessSetParams.AccessLevel)
		assert.Equal(t, []string{"C1", "C2"}, m.accessSetParams.ChannelIDs)
		assert.Equal(t, []string{"U1"}, m.accessSetParams.UserIDs)
	})

	t.Run("invalid access_level", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasAccessSetHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id":    "F123",
			"access_level": "admin",
			"channel_ids":  "C1",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "invalid access_level")
	})

	t.Run("missing access_level", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasAccessSetHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id":   "F123",
			"channel_ids": "C1",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "access_level is required")
	})

	t.Run("no channels or users", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasAccessSetHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id":    "F123",
			"access_level": "read",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "at least one of channel_ids or user_ids")
	})

	t.Run("slack error propagates", func(t *testing.T) {
		m := &mockCanvasClient{accessSetErr: errors.New("access_denied")}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasAccessSetHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id":    "F123",
			"access_level": "read",
			"channel_ids":  "C1",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "access_denied")
	})
}

// --- canvas_access_delete ---------------------------------------------------

func TestUnitCanvasAccessDeleteHandler(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasAccessDeleteHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id":   "F123",
			"channel_ids": "C1",
			"user_ids":    "U1,U2",
		}))
		require.NoError(t, err)

		payload := resultJSON(t, res)
		assert.Equal(t, "F123", payload["canvas_id"])
		assert.Equal(t, "access_deleted", payload["status"])
		assert.Equal(t, []string{"C1"}, m.accessDeleteParams.ChannelIDs)
		assert.Equal(t, []string{"U1", "U2"}, m.accessDeleteParams.UserIDs)
	})

	t.Run("no channels or users", func(t *testing.T) {
		m := &mockCanvasClient{}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasAccessDeleteHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id": "F123",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "at least one of channel_ids or user_ids")
	})

	t.Run("slack error propagates", func(t *testing.T) {
		m := &mockCanvasClient{accessDeleteErr: errors.New("access_denied")}
		h := newCanvasTestHandler(m)

		res, err := h.CanvasAccessDeleteHandler(context.Background(), canvasRequest(map[string]any{
			"canvas_id":   "F123",
			"channel_ids": "C1",
		}))
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "access_denied")
	})
}
