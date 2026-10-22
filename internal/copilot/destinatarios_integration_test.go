//go:build integration

package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/contadores"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestDestinatariosIAHistorialPropioPostgres(t *testing.T) {
	admin, app := poolPersistencia(t, "DATABASE_URL"), poolPersistencia(t, "APP_DATABASE_URL")
	ctx := t.Context()
	s := nucleo.NewServicio(app, nil, nil, nil, nil)
	resolutor := auth.NuevoResolutorDestino(app, time.Hour)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	usuario, tercero := uuid.NewString(), uuid.NewString()
	numeroBase := int(uuid.New().ID()%8000000) + 1000000
	celulares := make(map[int]string)
	for _, u := range []string{usuario, tercero} {
		if _, err := s.AbrirSubcuentasDeUsuario(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	crear := func(nombre string, numero int) string {
		t.Helper()
		id := uuid.NewString()
		celulares[numero] = fmt.Sprintf("+57300%07d", numeroBase+numero)
		exec("INSERT INTO auth.partes(id,tipo) VALUES($1,'persona')", id)
		exec("INSERT INTO auth.personas(parte_id,tipo_documento,documento,nombre) VALUES($1,'CC',$2,$3)", id, uuid.NewString(), nombre)
		exec("INSERT INTO auth.credenciales(id,parte_id,correo,celular,celular_verificado_en,valor_derivado,estado) VALUES($1,$2,$3,$4,now(),'test','activo')", uuid.New(), id, uuid.NewString()+"@test.local", celulares[numero])
		t.Cleanup(func() {
			for _, tabla := range []string{"credenciales", "personas", "partes"} {
				columna := "parte_id"
				if tabla == "partes" {
					columna = "id"
				}
				if _, err := admin.Exec(context.Background(), "DELETE FROM auth."+tabla+" WHERE "+columna+"=$1", id); err != nil {
					t.Error(err)
				}
			}
		})
		if _, err := s.AbrirSubcuentasDeUsuario(ctx, id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	enviar := func(emisor, destino string) {
		t.Helper()
		tx, err := admin.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		tr := uuid.New()
		if _, err := tx.Exec(ctx, "INSERT INTO nucleo.transferencia(id,usuario_id,cuenta_origen,llave_destino,monto,idempotency_key,estado) VALUES($1,$2,$3,'+573009999999',100,$4,'confirmada')", tr, emisor, nucleo.NombreCuentaDisponible(emisor), uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		var entry int64
		if err := tx.QueryRow(ctx, "INSERT INTO nucleo.journal_entry(tipo,transferencia_id,ocurrido_en) VALUES('transferencia_interna',$1,now()) RETURNING id", tr).Scan(&entry); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO nucleo.posting(entry_id,cuenta_id,monto,moneda) VALUES($1,$2,100,'COP'),($1,$3,-100,'COP')", entry, nucleo.NombreCuentaRetenido(emisor), nucleo.NombreCuentaDisponible(destino)); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	jose := crear("José Rodríguez", 1001)
	for range 2 {
		enviar(usuario, jose)
	}
	ajeno := crear("José Privado", 1002)
	enviar(tercero, ajeno)
	sinEnvios := crear("José Nuevo", 1003)
	recibido := crear("José Entrante", 1004)
	enviar(recibido, usuario)
	noVerificado := crear("José NoVerificado", 1005)
	enviar(usuario, noVerificado)
	exec("UPDATE auth.credenciales SET celular_verificado_en=NULL WHERE parte_id=$1", noVerificado)
	enfriado := crear("José Enfriado", 1006)
	enviar(usuario, enfriado)
	exec("UPDATE auth.credenciales SET celular_cambiado_en=now() WHERE parte_id=$1", enfriado)
	suspendido := crear("José Suspendido", 1007)
	enviar(usuario, suspendido)
	exec("UPDATE auth.credenciales SET estado='suspendido' WHERE parte_id=$1", suspendido)
	combinado := crear("Jose\u0301 Ramírez", 1008)
	enviar(usuario, combinado)
	for i := range 6 {
		enviar(usuario, crear(fmt.Sprintf("Laura Contacto%d", i), 1010+i))
	}
	// Más de una página SQL, con contrapartes heredadas sin perfil de auth.
	for range 101 {
		destino := uuid.NewString()
		if _, err := s.AbrirSubcuentasDeUsuario(ctx, destino); err != nil {
			t.Fatal(err)
		}
		enviar(usuario, destino)
	}
	vistos := make(map[string]bool)
	cursor := ""
	for {
		ids, err := s.DestinatariosPrevios(ctx, usuario, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if vistos[id] || id <= cursor {
				t.Fatal("cursor repite o desordena destinatario")
			}
			vistos[id] = true
		}
		cursor = ids[len(ids)-1]
	}
	if len(vistos) != 112 {
		t.Fatalf("historial paginado: %d, quiero 112", len(vistos))
	}
	redisTest := miniredis.RunT(t)
	cliente := redis.NewClient(&redis.Options{Addr: redisTest.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = cliente.Close() })
	contador, err := contadores.NuevoRedis(cliente)
	if err != nil {
		t.Fatal(err)
	}
	pepper := bytes.Repeat([]byte{7}, 32)
	claves, _ := auth.NuevasClaves(pepper)
	refs, _ := NuevasReferenciasDestinatario(pepper)
	limite := auth.NuevoLimiteDestinatarios(contador, claves, 3, time.Hour)
	h := ConsultaDestinatarioIA(s, resolutor, limite, refs)
	catalogo, err := NuevoCatalogo(map[string]ManejadorHerramienta{"buscar_destinatario": h}, nil)
	if err != nil {
		t.Fatal(err)
	}
	conv := uuid.New()
	ctx = identidad.ConIdentidad(context.WithValue(ctx, claveConversacionCiclo{}, conv), identidad.Identidad{UsuarioID: usuario, ParteID: uuid.NewString()})
	type respuesta struct {
		Datos struct {
			Destinatarios []struct {
				Nombre, Celular string
				Referencia      string `json:"destinatario_ref"`
			}
			HayMas bool `json:"hay_mas"`
		}
	}
	llamar := func(args string) ([]byte, respuesta) {
		t.Helper()
		r, err := catalogo.Ejecutar(ctx, LlamadaIA{Nombre: "buscar_destinatario", Argumentos: []byte(args)})
		if err != nil {
			t.Fatal(err)
		}
		var res respuesta
		if err := json.Unmarshal(r.Datos, &res); err != nil {
			t.Fatal(err)
		}
		for _, secreto := range []string{usuario, jose, ajeno, sinEnvios, recibido, combinado, "+573001231001", "Rodríguez"} {
			if strings.Contains(string(r.Datos), secreto) {
				t.Fatalf("filtró %s: %s", secreto, r.Datos)
			}
		}
		return r.Datos, res
	}
	for _, nombre := range []string{"JOSE", "José", "Jose\u0301"} {
		raw, res := llamar(fmt.Sprintf(`{"nombre":%q}`, nombre))
		if len(res.Datos.Destinatarios) != 2 || res.Datos.HayMas {
			t.Fatalf("historial/acentos/deduplicado: %s", raw)
		}
		ids := map[string]bool{}
		for _, d := range res.Datos.Destinatarios {
			id, err := refs.Resolver(usuario, conv, d.Referencia)
			if err != nil {
				t.Fatal(err)
			}
			ids[id] = true
		}
		if !ids[jose] || !ids[combinado] {
			t.Fatal("devolvió destinatarios ajenos")
		}
	}
	if raw, res := llamar(`{"nombre":"Privado"}`); len(res.Datos.Destinatarios) != 0 || !bytes.Contains(raw, []byte(`"destinatarios":[]`)) {
		t.Fatalf("búsqueda global: %s", raw)
	}
	if raw, res := llamar(`{"nombre":"Rodriguez"}`); len(res.Datos.Destinatarios) != 0 {
		t.Fatalf("buscó apellido privado: %s", raw)
	}
	if raw, res := llamar(`{"nombre":"laura"}`); len(res.Datos.Destinatarios) != 5 || !res.Datos.HayMas {
		t.Fatalf("tope cinco: %s", raw)
	}
	// El primer destino se puede buscar por teléfono y también alguien sin envíos previos.
	for _, cel := range []string{celulares[1001], celulares[1003]} {
		raw, res := llamar(fmt.Sprintf(`{"celular":%q}`, cel))
		if len(res.Datos.Destinatarios) != 1 {
			t.Fatalf("celular: %s", raw)
		}
	}
	if raw, _ := llamar(`{"celular":"3009999999"}`); !bytes.Contains(raw, []byte("RecipientNotFound")) {
		t.Fatalf("inexistente: %s", raw)
	}
	if raw, _ := llamar(fmt.Sprintf(`{"celular":%q}`, celulares[1001])); !bytes.Contains(raw, []byte("TooManyRequests")) {
		t.Fatalf("límite: %s", raw)
	}
	propio, err := catalogo.Ejecutar(identidad.ConIdentidad(ctx, identidad.Identidad{UsuarioID: jose}), LlamadaIA{Nombre: "buscar_destinatario", Argumentos: []byte(fmt.Sprintf(`{"celular":%q}`, celulares[1001]))})
	if err != nil || !bytes.Contains(propio.Datos, []byte("SelfTransferNotAllowed")) {
		t.Fatalf("propio: %s %v", propio.Datos, err)
	}
	redisTest.FastForward(time.Hour)
	// La misma clave y TTL son utilizables por el limitador de la ruta HTTP.
	otroLimite := auth.NuevoLimiteDestinatarios(contador, claves, 3, time.Hour)
	if err := otroLimite.Exigir(ctx, usuario); err != nil {
		t.Fatal(err)
	}
	if raw, res := llamar(fmt.Sprintf(`{"celular":%q}`, celulares[1001])); len(res.Datos.Destinatarios) != 1 {
		t.Fatalf("TTL: %s", raw)
	}
	redisTest.Close()
	if raw, _ := llamar(fmt.Sprintf(`{"celular":%q}`, celulares[1001])); !bytes.Contains(raw, []byte("FunctionUnavailable")) {
		t.Fatalf("fallo cerrado: %s", raw)
	}
}
