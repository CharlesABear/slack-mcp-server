package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

// canvasDocumentType is the document_content type expected by the Slack canvas
// APIs. Slack currently only supports markdown content for canvases.
const canvasDocumentType = "markdown"

// canvasOpsValid is the full set of supported canvases.edit operations.
var canvasOpsValid = map[string]bool{
	"insert_after":    true,
	"insert_before":   true,
	"insert_at_start": true,
	"insert_at_end":   true,
	"replace":         true,
	"delete":          true,
}

// canvasOpsRequiringSection lists edit operations that act relative to an
// existing section and therefore require a section_id.
var canvasOpsRequiringSection = map[string]bool{
	"insert_after":  true,
	"insert_before": true,
	"replace":       true,
	"delete":        true,
}

// validCanvasSectionTypes is the set of section types accepted by
// canvases.sections.lookup criteria.
var validCanvasSectionTypes = map[string]bool{
	"any_header": true,
	"h1":         true,
	"h2":         true,
	"h3":         true,
}

// validCanvasAccessLevels is the set of access levels accepted by
// canvases.access.set.
var validCanvasAccessLevels = map[string]bool{
	"read":  true,
	"write": true,
}

type CanvasHandler struct {
	apiProvider *provider.ApiProvider
	logger      *zap.Logger
}

func NewCanvasHandler(apiProvider *provider.ApiProvider, logger *zap.Logger) *CanvasHandler {
	return &CanvasHandler{
		apiProvider: apiProvider,
		logger:      logger,
	}
}

// buildCanvasChange validates an edit operation and assembles the corresponding
// slack.CanvasChange. It enforces that section-relative operations carry a
// section_id and that content-producing operations carry markdown.
func buildCanvasChange(operation, sectionID, markdown string) (slack.CanvasChange, error) {
	if operation == "" {
		return slack.CanvasChange{}, errors.New("operation is required")
	}
	if !canvasOpsValid[operation] {
		return slack.CanvasChange{}, fmt.Errorf("invalid operation %q: must be one of insert_after, insert_before, insert_at_start, insert_at_end, replace, delete", operation)
	}
	if canvasOpsRequiringSection[operation] && sectionID == "" {
		return slack.CanvasChange{}, fmt.Errorf("section_id is required for operation %q", operation)
	}
	if operation != "delete" && markdown == "" {
		return slack.CanvasChange{}, fmt.Errorf("markdown is required for operation %q", operation)
	}

	change := slack.CanvasChange{
		Operation: operation,
		SectionID: sectionID,
	}
	// The delete operation removes a section and carries no content.
	if operation != "delete" {
		change.DocumentContent = slack.DocumentContent{
			Type:     canvasDocumentType,
			Markdown: markdown,
		}
	}
	return change, nil
}

// CanvasCreateHandler creates a new standalone canvas and returns its ID.
func (h *CanvasHandler) CanvasCreateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasCreateHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		h.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	title := request.GetString("title", "")
	markdown := request.GetString("markdown", "")
	if markdown == "" {
		return nil, errors.New("markdown is required")
	}

	h.logger.Debug("Request parameters", zap.String("title", title))

	canvasID, err := h.apiProvider.Slack().CreateCanvasContext(ctx, title, slack.DocumentContent{
		Type:     canvasDocumentType,
		Markdown: markdown,
	})
	if err != nil {
		h.logger.Error("CreateCanvasContext failed", zap.Error(err))
		return nil, err
	}

	h.logger.Debug("Created canvas", zap.String("canvas_id", canvasID))

	return marshalCanvasResult(map[string]any{
		"canvas_id": canvasID,
		"status":    "created",
	})
}

// CanvasChannelCreateHandler creates a new canvas inside a channel and returns its ID.
func (h *CanvasHandler) CanvasChannelCreateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasChannelCreateHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		h.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	channelID := request.GetString("channel_id", "")
	if channelID == "" {
		return nil, errors.New("channel_id is required")
	}

	title := request.GetString("title", "")
	markdown := request.GetString("markdown", "")
	if markdown == "" {
		return nil, errors.New("markdown is required")
	}

	h.logger.Debug("Request parameters",
		zap.String("channel_id", channelID),
		zap.String("title", title),
	)

	var options []slack.CreateChannelCanvasOption
	if title != "" {
		options = append(options, slack.CreateChannelCanvasOptionTitle(title))
	}

	canvasID, err := h.apiProvider.Slack().CreateChannelCanvasContext(ctx, channelID, slack.DocumentContent{
		Type:     canvasDocumentType,
		Markdown: markdown,
	}, options...)
	if err != nil {
		h.logger.Error("CreateChannelCanvasContext failed", zap.Error(err))
		return nil, err
	}

	h.logger.Debug("Created channel canvas",
		zap.String("canvas_id", canvasID),
		zap.String("channel_id", channelID),
	)

	return marshalCanvasResult(map[string]any{
		"canvas_id":  canvasID,
		"channel_id": channelID,
		"status":     "created",
	})
}

// CanvasEditHandler applies a single edit operation to an existing canvas.
func (h *CanvasHandler) CanvasEditHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasEditHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		h.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	canvasID := request.GetString("canvas_id", "")
	if canvasID == "" {
		return nil, errors.New("canvas_id is required")
	}

	operation := request.GetString("operation", "")
	sectionID := request.GetString("section_id", "")
	markdown := request.GetString("markdown", "")

	change, err := buildCanvasChange(operation, sectionID, markdown)
	if err != nil {
		h.logger.Error("Invalid canvas edit operation", zap.Error(err))
		return nil, err
	}

	h.logger.Debug("Request parameters",
		zap.String("canvas_id", canvasID),
		zap.String("operation", operation),
		zap.String("section_id", sectionID),
	)

	err = h.apiProvider.Slack().EditCanvasContext(ctx, slack.EditCanvasParams{
		CanvasID: canvasID,
		Changes:  []slack.CanvasChange{change},
	})
	if err != nil {
		h.logger.Error("EditCanvasContext failed", zap.Error(err))
		return nil, err
	}

	h.logger.Debug("Edited canvas", zap.String("canvas_id", canvasID), zap.String("operation", operation))

	return marshalCanvasResult(map[string]any{
		"canvas_id": canvasID,
		"operation": operation,
		"status":    "edited",
	})
}

// CanvasDeleteHandler deletes an existing canvas.
func (h *CanvasHandler) CanvasDeleteHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasDeleteHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		h.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	canvasID := request.GetString("canvas_id", "")
	if canvasID == "" {
		return nil, errors.New("canvas_id is required")
	}

	h.logger.Debug("Request parameters", zap.String("canvas_id", canvasID))

	if err := h.apiProvider.Slack().DeleteCanvasContext(ctx, canvasID); err != nil {
		h.logger.Error("DeleteCanvasContext failed", zap.Error(err))
		return nil, err
	}

	h.logger.Debug("Deleted canvas", zap.String("canvas_id", canvasID))

	return marshalCanvasResult(map[string]any{
		"canvas_id": canvasID,
		"status":    "deleted",
	})
}

// CanvasSectionsLookupHandler finds sections in a canvas matching the given criteria.
func (h *CanvasHandler) CanvasSectionsLookupHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasSectionsLookupHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		h.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	canvasID := request.GetString("canvas_id", "")
	if canvasID == "" {
		return nil, errors.New("canvas_id is required")
	}

	sectionTypesStr := request.GetString("section_types", "")
	containsText := request.GetString("contains_text", "")

	criteria := slack.LookupCanvasSectionsCriteria{}
	if sectionTypesStr != "" {
		sectionTypes := parseCommaSeparatedList(sectionTypesStr)
		for _, st := range sectionTypes {
			if !validCanvasSectionTypes[st] {
				return nil, fmt.Errorf("invalid section type %q: must be one of any_header, h1, h2, h3", st)
			}
		}
		criteria.SectionTypes = sectionTypes
	}
	if containsText != "" {
		criteria.ContainsText = containsText
	}

	if len(criteria.SectionTypes) == 0 && criteria.ContainsText == "" {
		return nil, errors.New("at least one of section_types or contains_text is required")
	}

	h.logger.Debug("Request parameters",
		zap.String("canvas_id", canvasID),
		zap.Strings("section_types", criteria.SectionTypes),
		zap.String("contains_text", containsText),
	)

	sections, err := h.apiProvider.Slack().LookupCanvasSectionsContext(ctx, slack.LookupCanvasSectionsParams{
		CanvasID: canvasID,
		Criteria: criteria,
	})
	if err != nil {
		h.logger.Error("LookupCanvasSectionsContext failed", zap.Error(err))
		return nil, err
	}

	h.logger.Debug("Looked up canvas sections", zap.String("canvas_id", canvasID), zap.Int("count", len(sections)))

	sectionIDs := make([]string, 0, len(sections))
	for _, s := range sections {
		sectionIDs = append(sectionIDs, s.ID)
	}

	return marshalCanvasResult(map[string]any{
		"canvas_id": canvasID,
		"sections":  sectionIDs,
		"count":     len(sectionIDs),
	})
}

// CanvasAccessSetHandler sets the access level on a canvas for the given channels and/or users.
func (h *CanvasHandler) CanvasAccessSetHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasAccessSetHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		h.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	canvasID := request.GetString("canvas_id", "")
	if canvasID == "" {
		return nil, errors.New("canvas_id is required")
	}

	accessLevel := request.GetString("access_level", "")
	if accessLevel == "" {
		return nil, errors.New("access_level is required")
	}
	if !validCanvasAccessLevels[accessLevel] {
		return nil, fmt.Errorf("invalid access_level %q: must be one of read, write", accessLevel)
	}

	channelIDs := parseCommaSeparatedList(request.GetString("channel_ids", ""))
	userIDs := parseCommaSeparatedList(request.GetString("user_ids", ""))
	if len(channelIDs) == 0 && len(userIDs) == 0 {
		return nil, errors.New("at least one of channel_ids or user_ids is required")
	}

	h.logger.Debug("Request parameters",
		zap.String("canvas_id", canvasID),
		zap.String("access_level", accessLevel),
		zap.Strings("channel_ids", channelIDs),
		zap.Strings("user_ids", userIDs),
	)

	err := h.apiProvider.Slack().SetCanvasAccessContext(ctx, slack.SetCanvasAccessParams{
		CanvasID:    canvasID,
		AccessLevel: accessLevel,
		ChannelIDs:  channelIDs,
		UserIDs:     userIDs,
	})
	if err != nil {
		h.logger.Error("SetCanvasAccessContext failed", zap.Error(err))
		return nil, err
	}

	h.logger.Debug("Set canvas access", zap.String("canvas_id", canvasID), zap.String("access_level", accessLevel))

	return marshalCanvasResult(map[string]any{
		"canvas_id":    canvasID,
		"access_level": accessLevel,
		"status":       "access_set",
	})
}

// CanvasAccessDeleteHandler removes access to a canvas for the given channels and/or users.
func (h *CanvasHandler) CanvasAccessDeleteHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasAccessDeleteHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		h.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	canvasID := request.GetString("canvas_id", "")
	if canvasID == "" {
		return nil, errors.New("canvas_id is required")
	}

	channelIDs := parseCommaSeparatedList(request.GetString("channel_ids", ""))
	userIDs := parseCommaSeparatedList(request.GetString("user_ids", ""))
	if len(channelIDs) == 0 && len(userIDs) == 0 {
		return nil, errors.New("at least one of channel_ids or user_ids is required")
	}

	h.logger.Debug("Request parameters",
		zap.String("canvas_id", canvasID),
		zap.Strings("channel_ids", channelIDs),
		zap.Strings("user_ids", userIDs),
	)

	err := h.apiProvider.Slack().DeleteCanvasAccessContext(ctx, slack.DeleteCanvasAccessParams{
		CanvasID:   canvasID,
		ChannelIDs: channelIDs,
		UserIDs:    userIDs,
	})
	if err != nil {
		h.logger.Error("DeleteCanvasAccessContext failed", zap.Error(err))
		return nil, err
	}

	h.logger.Debug("Deleted canvas access", zap.String("canvas_id", canvasID))

	return marshalCanvasResult(map[string]any{
		"canvas_id": canvasID,
		"status":    "access_deleted",
	})
}

// marshalCanvasResult serializes a canvas tool result payload to a JSON text result.
func marshalCanvasResult(payload map[string]any) (*mcp.CallToolResult, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(jsonBytes)), nil
}
