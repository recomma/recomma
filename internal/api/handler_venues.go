package api

import (
	"context"
	"errors"
	"log/slog"
	"strings"
)

var (
	ErrVenueNotFound           = errors.New("api: venue not found")
	ErrVenueImmutable          = errors.New("api: venue immutable")
	ErrVenueInvalid            = errors.New("api: invalid venue payload")
	ErrVenueAssignmentNotFound = errors.New("api: venue assignment not found")
)

// ListVenues satisfies StrictServerInterface.
func (h *ApiHandler) ListVenues(ctx context.Context, req ListVenuesRequestObject) (ListVenuesResponseObject, error) {
	venues, err := h.store.ListVenues(ctx)
	if err != nil {
		if h.logger != nil {
			h.logger.ErrorContext(ctx, "ListVenues failed", slog.String("error", err.Error()))
		}
		return ListVenues500Response{}, nil
	}
	return ListVenues200JSONResponse{Items: venues}, nil
}

// UpsertVenue satisfies StrictServerInterface.
func (h *ApiHandler) UpsertVenue(ctx context.Context, req UpsertVenueRequestObject) (UpsertVenueResponseObject, error) {
	venueID := strings.TrimSpace(req.VenueId)
	if venueID == "" || req.Body == nil {
		return UpsertVenue400Response{}, nil
	}

	record, err := h.store.UpsertVenue(ctx, venueID, VenueUpsertRequest(*req.Body))
	if err != nil {
		switch {
		case errors.Is(err, ErrVenueInvalid):
			return UpsertVenue400Response{}, nil
		default:
			if h.logger != nil {
				h.logger.ErrorContext(ctx, "UpsertVenue failed", slog.String("venue", venueID), slog.String("error", err.Error()))
			}
			return UpsertVenue500Response{}, nil
		}
	}

	return UpsertVenue200JSONResponse(record), nil
}

// DeleteVenue satisfies StrictServerInterface.
func (h *ApiHandler) DeleteVenue(ctx context.Context, req DeleteVenueRequestObject) (DeleteVenueResponseObject, error) {
	venueID := strings.TrimSpace(req.VenueId)
	if venueID == "" {
		return DeleteVenue400Response{}, nil
	}

	err := h.store.DeleteVenue(ctx, venueID)
	if err != nil {
		switch {
		case errors.Is(err, ErrVenueNotFound):
			return DeleteVenue404Response{}, nil
		case errors.Is(err, ErrVenueImmutable):
			return DeleteVenue409Response{}, nil
		case errors.Is(err, ErrVenueInvalid):
			return DeleteVenue400Response{}, nil
		default:
			if h.logger != nil {
				h.logger.ErrorContext(ctx, "DeleteVenue failed", slog.String("venue", venueID), slog.String("error", err.Error()))
			}
			return DeleteVenue500Response{}, nil
		}
	}

	return DeleteVenue204Response{}, nil
}

// ListVenueAssignments satisfies StrictServerInterface.
func (h *ApiHandler) ListVenueAssignments(ctx context.Context, req ListVenueAssignmentsRequestObject) (ListVenueAssignmentsResponseObject, error) {
	venueID := strings.TrimSpace(req.VenueId)
	if venueID == "" {
		return ListVenueAssignments400Response{}, nil
	}

	assignments, err := h.store.ListVenueAssignments(ctx, venueID)
	if err != nil {
		if errors.Is(err, ErrVenueNotFound) {
			return ListVenueAssignments404Response{}, nil
		}
		if h.logger != nil {
			h.logger.ErrorContext(ctx, "ListVenueAssignments failed", slog.String("venue", venueID), slog.String("error", err.Error()))
		}
		return ListVenueAssignments500Response{}, nil
	}

	return ListVenueAssignments200JSONResponse{Items: assignments}, nil
}

// UpsertVenueAssignment satisfies StrictServerInterface.
func (h *ApiHandler) UpsertVenueAssignment(ctx context.Context, req UpsertVenueAssignmentRequestObject) (UpsertVenueAssignmentResponseObject, error) {
	venueID := strings.TrimSpace(req.VenueId)
	if venueID == "" || req.Body == nil {
		return UpsertVenueAssignment400Response{}, nil
	}

	botID := req.BotId
	if botID <= 0 {
		return UpsertVenueAssignment400Response{}, nil
	}

	record, err := h.store.UpsertVenueAssignment(ctx, venueID, botID, req.Body.IsPrimary)
	if err != nil {
		switch {
		case errors.Is(err, ErrVenueNotFound):
			return UpsertVenueAssignment404Response{}, nil
		case errors.Is(err, ErrVenueInvalid):
			return UpsertVenueAssignment400Response{}, nil
		default:
			if h.logger != nil {
				h.logger.ErrorContext(ctx, "UpsertVenueAssignment failed", slog.String("venue", venueID), slog.Int64("bot_id", botID), slog.String("error", err.Error()))
			}
			return UpsertVenueAssignment500Response{}, nil
		}
	}

	return UpsertVenueAssignment200JSONResponse(record), nil
}

// DeleteVenueAssignment satisfies StrictServerInterface.
func (h *ApiHandler) DeleteVenueAssignment(ctx context.Context, req DeleteVenueAssignmentRequestObject) (DeleteVenueAssignmentResponseObject, error) {
	venueID := strings.TrimSpace(req.VenueId)
	if venueID == "" {
		return DeleteVenueAssignment400Response{}, nil
	}
	botID := req.BotId
	if botID <= 0 {
		return DeleteVenueAssignment400Response{}, nil
	}

	err := h.store.DeleteVenueAssignment(ctx, venueID, botID)
	if err != nil {
		switch {
		case errors.Is(err, ErrVenueAssignmentNotFound):
			return DeleteVenueAssignment404Response{}, nil
		case errors.Is(err, ErrVenueInvalid):
			return DeleteVenueAssignment400Response{}, nil
		default:
			if h.logger != nil {
				h.logger.ErrorContext(ctx, "DeleteVenueAssignment failed", slog.String("venue", venueID), slog.Int64("bot_id", botID), slog.String("error", err.Error()))
			}
			return DeleteVenueAssignment500Response{}, nil
		}
	}

	return DeleteVenueAssignment204Response{}, nil
}

// ListBotVenues satisfies StrictServerInterface.
func (h *ApiHandler) ListBotVenues(ctx context.Context, req ListBotVenuesRequestObject) (ListBotVenuesResponseObject, error) {
	botID := req.BotId
	if botID <= 0 {
		return ListBotVenues400Response{}, nil
	}

	assignments, err := h.store.ListBotVenues(ctx, botID)
	if err != nil {
		if errors.Is(err, ErrVenueInvalid) {
			return ListBotVenues400Response{}, nil
		}
		if h.logger != nil {
			h.logger.ErrorContext(ctx, "ListBotVenues failed", slog.Int64("bot_id", botID), slog.String("error", err.Error()))
		}
		return ListBotVenues500Response{}, nil
	}

	return ListBotVenues200JSONResponse{Items: assignments}, nil
}
