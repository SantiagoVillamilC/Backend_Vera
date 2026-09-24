//go:build integration

package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// sembrarConsentimientoDeParte inserta una fila del log como rol_auth con el texto del catalogo.
func sembrarConsentimientoDeParte(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID, tipo string, aceptado bool, ocurridoEn pgtype.Timestamptz) {
	tb.Helper()

	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			texto, err := q.TextoDeConsentimiento(ctx, repositorio.TextoDeConsentimientoParams{Tipo: tipo, Version: "v1"})
			if err != nil {
				return struct{}{}, err
			}
			accion := AccionConsentimientoRevocar
			if aceptado {
				accion = AccionConsentimientoOtorgar
			}
			_, err = q.CrearConsentimiento(ctx, repositorio.CrearConsentimientoParams{
				ID:           uuidOPgtype(uuid.New()),
				ParteID:      uuidOPgtype(parteID),
				Tipo:         tipo,
				VersionTexto: texto.Version,
				Texto:        texto.Texto,
				Aceptado:     aceptado,
				Accion:       accion,
				Actor:        uuidOPgtype(parteID),
				Origen:       "10.0.0.1",
				UserAgent:    "test",
				Metodo:       "app",
				OcurridoEn:   ocurridoEn,
			})
			return struct{}{}, err
		}); err != nil {
		tb.Fatalf("sembrar consentimiento %s: %v", tipo, err)
	}
}

// Revocar el opt-in de WhatsApp deja OptInVigente en false y CrearDesafio responde ConsentRequired sin entregar.
func TestCrearDesafioConOptInRevocadoNoEntrega(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	cred := sembrarCredencialDeDesafio(t, pool, true)
	lector := NuevoLectorOptInDeConsentimientos(pool)

	ahora := time.Now().UTC()
	sembrarConsentimientoDeParte(t, pool, cred.parteID, TipoConsentimientoWhatsApp, true, pgtype.Timestamptz{Time: ahora, Valid: true})
	sembrarConsentimientoDeParte(t, pool, cred.parteID, TipoConsentimientoWhatsApp, false, pgtype.Timestamptz{Time: ahora.Add(time.Second), Valid: true})

	vigente, err := lector.OptInVigente(t.Context(), cred.parteID)
	if err != nil {
		t.Fatalf("OptInVigente: %v", err)
	}
	if vigente {
		t.Fatal("la ultima fila revocada debe dejar el opt-in en false")
	}

	canal := &canalOTPFalso{}
	gestor := gestorDeDesafio(t, pool, canal, lector)
	_, err = gestor.CrearDesafio(t.Context(), Solicitud{
		ParteID:      cred.parteID,
		CredencialID: cred.credencialID,
		Proposito:    PropositoLogin,
		Celular:      cred.celular,
		Origen:       "127.0.0.1",
	})
	if !errors.Is(err, ErrConsentimientoRequerido) {
		t.Fatalf("CrearDesafio = %v, quiero ErrConsentimientoRequerido", err)
	}
	if canal.ultimoCelular != "" {
		t.Fatalf("el simulador recibio %q, quiero ningun envio", canal.ultimoCelular)
	}
	if contarDesafiosDeParte(t, pool, cred.parteID) != 0 {
		t.Fatal("no debe quedar desafio persistido cuando falta el opt-in")
	}
}

// Volver a otorgar despues de revocar deja el opt-in vigente y el desafio se crea y se entrega.
func TestCrearDesafioTrasVolverAOtorgarEntrega(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	cred := sembrarCredencialDeDesafio(t, pool, true)
	lector := NuevoLectorOptInDeConsentimientos(pool)

	base := time.Now().UTC()
	sembrarConsentimientoDeParte(t, pool, cred.parteID, TipoConsentimientoWhatsApp, false, pgtype.Timestamptz{Time: base, Valid: true})
	sembrarConsentimientoDeParte(t, pool, cred.parteID, TipoConsentimientoWhatsApp, true, pgtype.Timestamptz{Time: base.Add(time.Second), Valid: true})

	vigente, err := lector.OptInVigente(t.Context(), cred.parteID)
	if err != nil {
		t.Fatalf("OptInVigente: %v", err)
	}
	if !vigente {
		t.Fatal("otorgar despues de revocar debe dejar el opt-in en true")
	}

	canal := &canalOTPFalso{}
	gestor := gestorDeDesafio(t, pool, canal, lector)
	desafio, err := gestor.CrearDesafio(t.Context(), Solicitud{
		ParteID:      cred.parteID,
		CredencialID: cred.credencialID,
		Proposito:    PropositoLogin,
		Celular:      cred.celular,
		Origen:       "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("CrearDesafio: %v", err)
	}
	if desafio.ID == uuid.Nil {
		t.Fatal("quiero un desafio con id")
	}
	if canal.ultimoCelular != cred.celular {
		t.Fatalf("el simulador recibio %q, quiero %q", canal.ultimoCelular, cred.celular)
	}
}

// El texto del catalogo viaja en la fila: la prueba de lo presentado queda sin depender del cliente.
func TestConsentimientoGuardaElTextoExactoDelCatalogo(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	parteID := uuid.New()
	sembrarCredencialDeDesafioParaParte(t, pool, parteID)

	servicio := NuevoServicioDeConsentimientos(pool)
	consentimiento, err := servicio.Registrar(t.Context(), SolicitudDeConsentimientoNuevo{
		ParteID:      parteID,
		Tipo:         TipoConsentimientoTratamiento,
		VersionTexto: "v1",
		Aceptado:     true,
		Metodo:       "",
		Origen:       "10.0.0.1",
		UserAgent:    "  " + strings.Repeat("a", 600),
	})
	if err != nil {
		t.Fatalf("Registrar: %v", err)
	}
	if consentimiento.Texto == "" || consentimiento.VersionTexto != "v1" {
		t.Fatalf("consentimiento = %+v, quiero texto y version del catalogo", consentimiento)
	}
	if consentimiento.Metodo != MetodoConsentimientoPorDefecto {
		t.Fatalf("metodo = %q, quiero %q", consentimiento.Metodo, MetodoConsentimientoPorDefecto)
	}
	if len([]rune(consentimiento.UserAgent)) > maxRunasUserAgent {
		t.Fatalf("user_agent tiene %d runas, quiero tope %d", len([]rune(consentimiento.UserAgent)), maxRunasUserAgent)
	}
}

// sembrarCredencialDeDesafioParaParte crea una parte y su credencial minima para las pruebas de consentimiento.
func sembrarCredencialDeDesafioParaParte(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID) {
	tb.Helper()
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			if _, err := q.CrearParte(ctx, repositorio.CrearParteParams{ID: uuidOPgtype(parteID), Tipo: "persona"}); err != nil {
				return struct{}{}, err
			}
			_, err := q.CrearCredencial(ctx, repositorio.CrearCredencialParams{
				ID:            uuidOPgtype(uuid.New()),
				ParteID:       uuidOPgtype(parteID),
				Correo:        uuid.NewString() + "@centinela.test",
				Celular:       "+57300" + strings.ReplaceAll(uuid.NewString(), "-", "")[:7],
				ValorDerivado: pgtype.Text{String: "$argon2id$hash", Valid: true},
				Estado:        "activo",
			})
			return struct{}{}, err
		}); err != nil {
		tb.Fatalf("sembrar credencial de consentimiento: %v", err)
	}
}
