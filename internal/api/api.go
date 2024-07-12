package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"journey/internal/api/spec"
	"journey/internal/pgstore"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type mailer interface {
	SendConfirmTripEmailToTripOwner(tripID uuid.UUID) error
}

type store interface {
	CreateTrip(context.Context, *pgxpool.Pool, spec.CreateTripRequest) (uuid.UUID, error)
	GetParticipant(ctx context.Context, participantID uuid.UUID) (pgstore.Participant, error)
	ConfirmParticipant(ctx context.Context, participantID uuid.UUID) error
	GetTrip(ctx context.Context, tripID uuid.UUID) (pgstore.Trip, error)
	UpdateTrip(ctx context.Context, body pgstore.UpdateTripParams) error
	GetTripActivities(ctx context.Context, tripID uuid.UUID) ([]pgstore.Activity, error)
	CreateActivity(ctx context.Context, arg pgstore.CreateActivityParams) (uuid.UUID, error)
	GetTripLinks(ctx context.Context, tripID uuid.UUID) ([]pgstore.Link, error)
}

type API struct {
	store     store
	logger    *zap.Logger
	validator *validator.Validate
	pool      *pgxpool.Pool
	mailer    mailer
}

// Confirms a participant on a trip.
// (PATCH /participants/{participantId}/confirm)
func (api *API) PatchParticipantsParticipantIDConfirm(w http.ResponseWriter, r *http.Request, participantID string) *spec.Response {
	id, err := uuid.Parse(participantID)
	if err != nil {
		return spec.PatchParticipantsParticipantIDConfirmJSON400Response(spec.Error{Message: "uuid inválido"})
	}

	participant, err := api.store.GetParticipant(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return spec.PatchParticipantsParticipantIDConfirmJSON400Response(
				spec.Error{Message: "participante não encontrado"},
			)
		}
		api.logger.Error("failed to get participant", zap.Error(err), zap.String("participant_id", participantID))
		return spec.PatchParticipantsParticipantIDConfirmJSON400Response(
			spec.Error{Message: "something went wrong, try again"},
		)
	}

	if participant.IsConfirmed {
		return spec.PatchParticipantsParticipantIDConfirmJSON400Response(
			spec.Error{Message: "participante já confirmado"},
		)
	}

	if err := api.store.ConfirmParticipant(r.Context(), id); err != nil {
		api.logger.Error("failed to confirm participant", zap.Error(err), zap.String("participant_id", participantID))
		return spec.PatchParticipantsParticipantIDConfirmJSON400Response(
			spec.Error{Message: "something went wrong, try again"},
		)
	}

	return spec.PatchParticipantsParticipantIDConfirmJSON204Response(nil)
}

func NewAPI(pool *pgxpool.Pool, logger *zap.Logger, mailer mailer) API {
	validator := validator.New(validator.WithRequiredStructEnabled())
	return API{pgstore.New(pool), logger, validator, pool, mailer}
}

// Create a new trip
// (POST /trips)
func (api *API) PostTrips(w http.ResponseWriter, r *http.Request) *spec.Response {
	var body spec.CreateTripRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return spec.PostTripsJSON400Response(spec.Error{Message: "invalid JSON"})
	}

	if err := api.validator.Struct(body); err != nil {
		return spec.PostTripsJSON400Response(spec.Error{Message: "invalid input: " + err.Error()})
	}

	tripID, err := api.store.CreateTrip(r.Context(), api.pool, body)

	go func() {
		if err := api.mailer.SendConfirmTripEmailToTripOwner(tripID); err != nil {
			api.logger.Error(
				"failed to send email on PostTrips",
				zap.Error(err),
				zap.String("trip_id", tripID.String()))
		}
	}()
	if err != nil {
		api.logger.Error("failed to create trip", zap.Error(err))
		return spec.PostTripsJSON400Response(spec.Error{Message: "something went wrong, try again"})
	}

	return spec.PostTripsJSON201Response(spec.CreateTripResponse{TripID: tripID.String()})
}

// Get a trip details.
// (GET /trips/{tripId})
func (api *API) GetTripsTripID(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	id, err := uuid.Parse(tripID)
	if err != nil {
		api.logger.Error("failed to parse trip id", zap.Error(err), zap.String("trip_id", tripID))
		return spec.GetTripsTripIDJSON400Response(spec.Error{Message: "invalid trip id"})
	}

	trip, err := api.store.GetTrip(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return spec.GetTripsTripIDJSON400Response(spec.Error{Message: "trip not found"})
		}
		api.logger.Error("failed to get trip", zap.Error(err), zap.String("trip_id", tripID))
		return spec.GetTripsTripIDJSON400Response(spec.Error{Message: "something went wrong, try again"})
	}

	return spec.GetTripsTripIDJSON200Response(spec.GetTripDetailsResponse{
		Trip: spec.GetTripDetailsResponseTripObj{
			Destination: trip.Destination,
			EndsAt:      trip.EndsAt.Time,
			ID:          tripID,
			IsConfirmed: trip.IsConfirmed,
			StartsAt:    trip.StartsAt.Time,
		},
	})
}

// Update a trip.
// (PUT /trips/{tripId})
func (api *API) PutTripsTripID(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	id, err := uuid.Parse(tripID)
	if err != nil {
		api.logger.Error("failed to parse trip id", zap.Error(err), zap.String("trip_id", tripID))
		return spec.PutTripsTripIDJSON400Response(spec.Error{Message: "invalid trip id"})
	}

	trip, err := api.store.GetTrip(r.Context(), id)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return spec.PutTripsTripIDJSON400Response(spec.Error{Message: "trip not found"})
		}
		api.logger.Error("failed to get trip", zap.Error(err), zap.String("trip_id", tripID))
		return spec.PutTripsTripIDJSON400Response(spec.Error{Message: "something went wrong, try again"})
	}

	var body spec.UpdateTripRequest

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return spec.PutTripsTripIDJSON400Response(spec.Error{Message: "invalid JSON"})
	}

	if err := api.validator.Struct(body); err != nil {
		return spec.PutTripsTripIDJSON400Response(spec.Error{Message: "invalid input: " + err.Error()})
	}

	if err := api.store.UpdateTrip(r.Context(), pgstore.UpdateTripParams{
		ID:          id,
		Destination: body.Destination,
		StartsAt:    pgtype.Timestamp{Time: body.StartsAt, Valid: true},
		EndsAt:      pgtype.Timestamp{Time: body.EndsAt, Valid: true},
		IsConfirmed: trip.IsConfirmed,
	}); err != nil {
		api.logger.Error("failed to update trip", zap.Error(err))
		return spec.PutTripsTripIDJSON400Response(spec.Error{Message: "something went wrong, try again"})

	}

	return spec.PutTripsTripIDJSON204Response(nil)
}

// Get a trip activities.
// (GET /trips/{tripId}/activities)
func (api *API) GetTripsTripIDActivities(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	id, err := uuid.Parse(tripID)
	if err != nil {
		api.logger.Error("failed to parse trip id", zap.Error(err), zap.String("trip_id", tripID))
		return spec.GetTripsTripIDActivitiesJSON400Response(spec.Error{Message: "invalid trip id"})
	}

	activities, err := api.store.GetTripActivities(r.Context(), id)
	if err != nil {
		api.logger.Error("failed to get trip activities", zap.Error(err), zap.String("trip_id", tripID))
		if errors.Is(err, pgx.ErrNoRows) {
			return spec.GetTripsTripIDActivitiesJSON400Response(spec.Error{Message: "trip not found"})
		}
		return spec.GetTripsTripIDActivitiesJSON400Response(spec.Error{Message: "something went wrong, try again"})
	}

	var output spec.GetTripActivitiesResponse

	groupedActivities := make(map[string][]pgstore.Activity)

	for _, activity := range activities {
		date := activity.OccursAt.Time.Format(time.DateOnly)
		groupedActivities[date] = append(groupedActivities[date], activity)
	}

	for dateStr, actsOnDate := range groupedActivities {
		var innerActs []spec.GetTripActivitiesResponseInnerArray

		for _, act := range actsOnDate {
			innerActs = append(innerActs, spec.GetTripActivitiesResponseInnerArray{
				ID:       act.ID.String(),
				Title:    act.Title,
				OccursAt: act.OccursAt.Time,
			})
		}

		date, _ := time.Parse(time.DateOnly, dateStr)
		output.Activities = append(output.Activities, spec.GetTripActivitiesResponseOuterArray{
			Date:       date,
			Activities: innerActs,
		})
	}

	return spec.GetTripsTripIDActivitiesJSON200Response(output)
}

// Create a trip activity.
// (POST /trips/{tripId}/activities)
func (api *API) PostTripsTripIDActivities(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	id, err := uuid.Parse(tripID)
	if err != nil {
		api.logger.Error("failed to parse trip id", zap.Error(err), zap.String("trip_id", tripID))
		return spec.PostTripsTripIDActivitiesJSON400Response(spec.Error{Message: "invalid trip id"})
	}

	var body spec.CreateActivityRequest

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return spec.PostTripsTripIDActivitiesJSON400Response(spec.Error{Message: "invalid JSON"})
	}

	if err := api.validator.Struct(body); err != nil {
		return spec.PostTripsTripIDActivitiesJSON400Response(spec.Error{Message: "invalid input: " + err.Error()})
	}

	activityID, err := api.store.CreateActivity(r.Context(), pgstore.CreateActivityParams{
		TripID:   id,
		Title:    body.Title,
		OccursAt: pgtype.Timestamp{Time: body.OccursAt, Valid: true},
	})

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return spec.PostTripsTripIDActivitiesJSON400Response(spec.Error{Message: "trip not found"})
		}
		api.logger.Error("failed to create activity", zap.Error(err))
		return spec.PostTripsTripIDActivitiesJSON400Response(spec.Error{Message: "something went wrong, try again"})
	}

	return spec.PostTripsTripIDActivitiesJSON201Response(spec.CreateActivityResponse{ActivityID: activityID.String()})
}

// Confirm a trip and send e-mail invitations.
// (GET /trips/{tripId}/confirm)
func (api *API) GetTripsTripIDConfirm(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	panic("not implemented") // TODO: Implement
}

// Invite someone to the trip.
// (POST /trips/{tripId}/invites)
func (api *API) PostTripsTripIDInvites(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	panic("not implemented") // TODO: Implement
}

// Get a trip links.
// (GET /trips/{tripId}/links)
func (api *API) GetTripsTripIDLinks(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	id, err := uuid.Parse(tripID)
	if err != nil {
		return spec.GetTripsTripIDLinksJSON400Response(spec.Error{Message: "invalid trip id"})
	}

	links, err := api.store.GetTripLinks(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return spec.GetTripsTripIDLinksJSON400Response(spec.Error{Message: "trip not found"})
		}
		api.logger.Error("failed to get trip links", zap.Error(err), zap.String("trip_id", tripID))
		return spec.GetTripsTripIDLinksJSON400Response(spec.Error{Message: "something went wrong, try again"})
	}

	var output spec.GetLinksResponse

	for _, link := range links {
		output.Links = append(output.Links, spec.GetLinksResponseArray{
			ID:    link.ID.String(),
			Title: link.Title,
			URL:   link.Url,
		})
	}

	return spec.GetTripsTripIDLinksJSON200Response(output)
}

// Create a trip link.
// (POST /trips/{tripId}/links)
func (api *API) PostTripsTripIDLinks(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	panic("not implemented") // TODO: Implement
}

// Get a trip participants.
// (GET /trips/{tripId}/participants)
func (api *API) GetTripsTripIDParticipants(w http.ResponseWriter, r *http.Request, tripID string) *spec.Response {
	panic("not implemented") // TODO: Implement
}
