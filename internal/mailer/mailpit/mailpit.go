package mailpit

import (
	"context"
	"fmt"
	"journey/internal/pgstore"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wneessen/go-mail"
)

type store interface {
	GetTrip(context.Context, uuid.UUID) (pgstore.Trip, error)
	GetParticipants(context.Context, uuid.UUID) ([]pgstore.Participant, error)
}

type MailPit struct {
	store store
}

func NewMailPit(pool *pgxpool.Pool) MailPit {
	return MailPit{pgstore.New(pool)}
}

func (m MailPit) SendConfirmTripEmailToTripOwner(tripId uuid.UUID) error {
	ctx := context.Background()
	trip, err := m.store.GetTrip(ctx, tripId)
	if err != nil {
		return fmt.Errorf("mailpit: failed to get trip for SendConfirmTripEmailToTripOwner: %w", err)
	}

	msg := mail.NewMsg()
	if err := msg.From("mailpit@journey.com"); err != nil {
		return fmt.Errorf("mailpit: failed to set From in email for SendConfirmTripEmailToTripOwner: %w", err)
	}

	if err := msg.To(trip.OwnerEmail); err != nil {
		return fmt.Errorf("mailpit: failed to set To in email for SendConfirmTripEmailToTripOwner: %w", err)
	}

	msg.Subject("Confirme sua viagem")

	msg.SetBodyString(mail.TypeTextPlain, fmt.Sprintf(`
    Olá, %s!
    A sua viagem para %s que começa no dia %s precisa ser confirmada.
    Clique no botão abaixo para confirmar.
    `,
		trip.OwnerName,
		trip.Destination,
		trip.StartsAt.Time.Format(time.DateOnly),
	))

	client, err := mail.NewClient("mailpit", mail.WithTLSPortPolicy(mail.NoTLS), mail.WithPort(1025))

	if err != nil {
		return fmt.Errorf("mailpit: failed to create mail client for SendConfirmTripEmailToTripOwner: %w", err)
	}

	if err := client.DialAndSend(msg); err != nil {
		return fmt.Errorf("mailpit: failed to send email for SendConfirmTripEmailToTripOwner: %w", err)
	}

	return nil
}

func (m MailPit) SendTripConfirmedEmails(tripId uuid.UUID) error {
	ctx := context.Background()
	participants, err := m.store.GetParticipants(ctx, tripId)
	if err != nil {
		return fmt.Errorf("mailpit: failed to get trip participants for SendTripConfirmedEmails: %w", err)
	}

	client, err := mail.NewClient("mailpit", mail.WithTLSPortPolicy(mail.NoTLS), mail.WithPort(1025))
	if err != nil {
		return fmt.Errorf("mailpit: failed to create mail client for SendTripConfirmedEmails: %w", err)
	}

	for _, participant := range participants {
		msg := mail.NewMsg()
		if err := msg.From("mailpit@journey.com"); err != nil {
			return fmt.Errorf("mailpit: failed to set From in email for SendTripConfirmedEmails: %w", err)
		}

		if err := msg.To(participant.Email); err != nil {
			return fmt.Errorf("mailpit: failed to set To in email for SendTripConfirmedEmails: %w", err)
		}

		msg.Subject("Confirme sua viagem")

		msg.SetBodyString(mail.TypeTextPlain, "Você deve confirmar a sua viagem")

		if err := client.DialAndSend(msg); err != nil {
			return fmt.Errorf("mailpit: failed to send email for SendTripConfirmedEmails: %w", err)
		}
	}

	return nil
}
