package inteligencia

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrLoteEnCurso reporta que otro proceso ya tiene el lote de enriquecimiento.
var ErrLoteEnCurso = errors.New("inteligencia: el lote de enriquecimiento ya está corriendo")

// VersionModelo identifica las reglas con las que se calculó una fila de la copia.
const VersionModelo = "cascada-1:primer-corte"

// EspacioTransaccion es el espacio de nombres con el que se derivan los identificadores de las
// operaciones que no tienen transferencia_id en el ledger.
var EspacioTransaccion = uuid.MustParse("1f0f4f2e-6f2b-5f3a-9d5a-0b7c3a9e4d21")

// Resultado resume un lote.
type Resultado struct {
	Asientos            int
	Movimientos         int
	SinContraparte      int
	SinCategoria        int
	Abstenciones        int
	PorOrigen           map[string]int
	VersionClasificador string
}

// Enriquecer procesa un lote de asientos nuevos y escribe la copia enriquecida.
func (s *Servicio) Enriquecer(ctx context.Context, limite int) (Resultado, error) {
	if limite <= 0 {
		limite = 500
	}
	if limite > 5000 {
		limite = 5000
	}
	inicio := time.Now()
	resultado, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, tx transaccionDeInteligencia, q *repositorio.Queries) (Resultado, error) {
			bloqueado, err := intentarBloqueo(ctx, tx)
			if err != nil {
				return Resultado{}, err
			}
			if !bloqueado {
				return Resultado{}, ErrLoteEnCurso
			}

			cursor, err := q.LeerCursor(ctx)
			if err != nil {
				return Resultado{}, fmt.Errorf("inteligencia: leer cursor: %w", err)
			}
			anclas, err := q.AnclasPendientes(ctx, repositorio.AnclasPendientesParams{
				DespuesDe: cursor.UltimoEntryID,
				Limite:    int32(limite),
			})
			if err != nil {
				return Resultado{}, fmt.Errorf("inteligencia: leer asientos pendientes: %w", err)
			}
			if len(anclas) == 0 {
				return Resultado{PorOrigen: map[string]int{}, VersionClasificador: s.versionDelClasificador()}, nil
			}

			lineas, err := q.LineasDeAsientos(ctx, anclas)
			if err != nil {
				return Resultado{}, fmt.Errorf("inteligencia: leer líneas del lote: %w", err)
			}
			transferencias := transferenciasDelLote(lineas)
			var lineasDeTransferencias []repositorio.LineasDeTransferenciasRow
			if len(transferencias) > 0 {
				lineasDeTransferencias, err = q.LineasDeTransferencias(ctx, transferencias)
				if err != nil {
					return Resultado{}, fmt.Errorf("inteligencia: leer líneas de las transferencias: %w", err)
				}
			}

			grupos := construirGrupos(anclas, lineas, lineasDeTransferencias)
			nombres, err := resolverNombres(ctx, tx, grupos)
			if err != nil {
				return Resultado{}, err
			}
			memorias := make(map[string]Memoria)
			resultado := Resultado{
				Asientos:            len(anclas),
				PorOrigen:           map[string]int{},
				VersionClasificador: s.versionDelClasificador(),
			}
			for _, grupo := range grupos {
				transaccionID := transaccionDeGrupo(grupo)
				efectos, err := EfectosPorUsuario(grupo.apuntes)
				if err != nil {
					return Resultado{}, err
				}
				for _, efecto := range efectos {
					if efecto.Neto == 0 {
						if err := q.BorrarMovimientoEnriquecido(ctx, repositorio.BorrarMovimientoEnriquecidoParams{
							UsuarioID:     efecto.UsuarioID,
							TransaccionID: pgtype.UUID{Bytes: transaccionID, Valid: true},
						}); err != nil {
							return Resultado{}, fmt.Errorf("inteligencia: borrar movimiento compensado: %w", err)
						}
						continue
					}
					memoria, cargada := memorias[efecto.UsuarioID]
					if !cargada {
						memoria, err = memoriaDeUsuario(ctx, q, efecto.UsuarioID)
						if err != nil {
							return Resultado{}, err
						}
						memorias[efecto.UsuarioID] = memoria
					}
					contraparte := ResolverContraparte(grupo.apuntes, efecto.UsuarioID, nombres)
					if err := escribirMovimiento(ctx, q, grupo, efecto, transaccionID, contraparte, memoria, s.clasificador, s.lexico.ParesDeLexico(), &resultado); err != nil {
						return Resultado{}, err
					}
				}
			}

			if err := q.AvanzarCursor(ctx, anclas[len(anclas)-1]); err != nil {
				return Resultado{}, fmt.Errorf("inteligencia: avanzar cursor: %w", err)
			}
			return resultado, nil
		})
	if err != nil {
		desenlace := DesenlaceLoteError
		if errors.Is(err, ErrLoteEnCurso) {
			desenlace = DesenlaceLoteEnCurso
		}
		s.metricas.LoteNoEjecutado(desenlace, time.Since(inicio))
		return Resultado{}, err
	}
	duracion := time.Since(inicio)
	s.metricas.LoteTerminado(resultado, duracion)
	slog.Info("inteligencia.enriquecimiento",
		"asientos", resultado.Asientos,
		"movimientos", resultado.Movimientos,
		"sin_contraparte", resultado.SinContraparte,
		"sin_categoria", resultado.SinCategoria,
		"abstenciones", resultado.Abstenciones,
		"por_origen", resultado.PorOrigen,
		"duracion_ms", duracion.Milliseconds())
	return resultado, nil
}

// Reconstruir borra la copia, reinicia el cursor y la vuelve a calcular. Devuelve la huella.
func (s *Servicio) Reconstruir(ctx context.Context, limite int) (string, error) {
	_, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (struct{}, error) {
			if err := q.BorrarCopia(ctx); err != nil {
				return struct{}{}, fmt.Errorf("inteligencia: borrar la copia: %w", err)
			}
			if err := q.ReiniciarCursor(ctx); err != nil {
				return struct{}{}, fmt.Errorf("inteligencia: reiniciar el cursor: %w", err)
			}
			return struct{}{}, nil
		})
	if err != nil {
		return "", err
	}
	for {
		resultado, err := s.Enriquecer(ctx, limite)
		if err != nil {
			return "", err
		}
		if resultado.Asientos == 0 {
			break
		}
	}
	return s.Huella(ctx)
}

// Huella devuelve el sha256 de la copia ordenada, para comparar una reconstrucción con la anterior.
func (s *Servicio) Huella(ctx context.Context) (string, error) {
	filas, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) ([]repositorio.ExportarCopiaRow, error) {
			return q.ExportarCopia(ctx)
		})
	if err != nil {
		return "", fmt.Errorf("inteligencia: exportar la copia: %w", err)
	}
	suma := sha256.New()
	for _, fila := range filas {
		transaccion := ""
		if fila.TransaccionID.Valid {
			transaccion = uuid.UUID(fila.TransaccionID.Bytes).String()
		}
		fmt.Fprintf(suma,
			"%s\x1f%s\x1f%d\x1f%s\x1f%s\x1f%s\x1f%d\x1f%s\x1f%t\x1f%s\x1f%s\x1f%s\x1f%s\x1f%s\x1f%s\x1f%s\x1f%s\x1f%s\x1f%s\n",
			fila.UsuarioID, transaccion, fila.EntryAncla,
			fila.OcurridoEn.Time.UTC().Format(time.RFC3339Nano), fila.Moneda, fila.Direccion,
			fila.MontoCentavos, fila.Contraparte, fila.ContraparteResuelta,
			fila.Descripcion.String, fila.Categoria, fila.OrigenCategoria,
			fila.ComercioMid.String, fila.ComercioNombre.String, fila.Mcc.String, fila.VersionModelo,
			fila.TipoDestino.String, fila.ContraparteLlave.String, fila.ContraparteNombreNormalizado.String)
	}
	return hex.EncodeToString(suma.Sum(nil)), nil
}

type grupo struct {
	clave              string
	transaccionID      uuid.UUID
	tieneTransferencia bool
	apuntes            []Apunte
	entryAncla         int64
	ocurridoEn         time.Time
	descripcion        string
}

func construirGrupos(anclas []int64, lineas []repositorio.LineasDeAsientosRow,
	lineasDeTransferencias []repositorio.LineasDeTransferenciasRow) []grupo {
	porAsiento := make(map[int64][]Apunte, len(anclas))
	for _, fila := range lineas {
		porAsiento[fila.EntryID] = append(porAsiento[fila.EntryID], apunteDeAsiento(fila))
	}
	porTransferencia := make(map[uuid.UUID][]Apunte)
	for _, fila := range lineasDeTransferencias {
		if !fila.TransferenciaID.Valid {
			continue
		}
		transaccion := uuid.UUID(fila.TransferenciaID.Bytes)
		porTransferencia[transaccion] = append(porTransferencia[transaccion], apunteDeTransferencia(fila))
	}

	vistos := make(map[string]struct{})
	grupos := make([]grupo, 0, len(anclas))
	for _, ancla := range anclas {
		apuntes := porAsiento[ancla]
		if len(apuntes) == 0 {
			continue
		}
		primero := apuntes[0]
		if primero.TieneTransferencia {
			clave := primero.TransferenciaID.String()
			if _, repetido := vistos[clave]; repetido {
				continue
			}
			vistos[clave] = struct{}{}
			completos := porTransferencia[primero.TransferenciaID]
			if len(completos) == 0 {
				completos = apuntes
			}
			grupos = append(grupos, nuevoGrupo(clave, primero.TransferenciaID, true, completos))
			continue
		}
		clave := "entry:" + strconv.FormatInt(ancla, 10)
		if _, repetido := vistos[clave]; repetido {
			continue
		}
		vistos[clave] = struct{}{}
		grupos = append(grupos, nuevoGrupo(clave, uuid.Nil, false, apuntes))
	}
	sort.Slice(grupos, func(i, j int) bool {
		if grupos[i].entryAncla != grupos[j].entryAncla {
			return grupos[i].entryAncla < grupos[j].entryAncla
		}
		return grupos[i].clave < grupos[j].clave
	})
	return grupos
}

func nuevoGrupo(clave string, transaccionID uuid.UUID, tieneTransferencia bool, apuntes []Apunte) grupo {
	ordenados := make([]Apunte, len(apuntes))
	copy(ordenados, apuntes)
	sort.Slice(ordenados, func(i, j int) bool { return ordenados[i].EntryID < ordenados[j].EntryID })
	ancla := ordenados[0].EntryID
	var ocurrido time.Time
	descripcion := ""
	for _, apunte := range ordenados {
		if apunte.EntryID < ancla {
			ancla = apunte.EntryID
		}
		if apunte.OcurridoEn.After(ocurrido) {
			ocurrido = apunte.OcurridoEn
		}
		if descripcion == "" && apunte.Descripcion != "" {
			descripcion = apunte.Descripcion
		}
	}
	return grupo{
		clave:              clave,
		transaccionID:      transaccionID,
		tieneTransferencia: tieneTransferencia,
		apuntes:            ordenados,
		entryAncla:         ancla,
		ocurridoEn:         ocurrido,
		descripcion:        descripcion,
	}
}

func apunteDeAsiento(fila repositorio.LineasDeAsientosRow) Apunte {
	apunte := Apunte{
		EntryID:     fila.EntryID,
		OcurridoEn:  fila.OcurridoEn.Time,
		Tipo:        fila.Tipo,
		Descripcion: fila.Descripcion.String,
		CuentaID:    fila.CuentaID,
		Monto:       fila.Monto,
		Moneda:      fila.Moneda,
	}
	if fila.TransferenciaID.Valid {
		apunte.TransferenciaID = uuid.UUID(fila.TransferenciaID.Bytes)
		apunte.TieneTransferencia = true
	}
	return apunte
}

func apunteDeTransferencia(fila repositorio.LineasDeTransferenciasRow) Apunte {
	return Apunte{
		EntryID:            fila.EntryID,
		TransferenciaID:    uuid.UUID(fila.TransferenciaID.Bytes),
		TieneTransferencia: fila.TransferenciaID.Valid,
		OcurridoEn:         fila.OcurridoEn.Time,
		Tipo:               fila.Tipo,
		Descripcion:        fila.Descripcion.String,
		CuentaID:           fila.CuentaID,
		Monto:              fila.Monto,
		Moneda:             fila.Moneda,
	}
}

func transferenciasDelLote(lineas []repositorio.LineasDeAsientosRow) []pgtype.UUID {
	vistas := make(map[uuid.UUID]struct{})
	transferencias := make([]pgtype.UUID, 0)
	for _, fila := range lineas {
		if !fila.TransferenciaID.Valid {
			continue
		}
		transaccion := uuid.UUID(fila.TransferenciaID.Bytes)
		if _, repetida := vistas[transaccion]; repetida {
			continue
		}
		vistas[transaccion] = struct{}{}
		transferencias = append(transferencias, fila.TransferenciaID)
	}
	return transferencias
}

func transaccionDeGrupo(g grupo) uuid.UUID {
	if g.tieneTransferencia {
		return g.transaccionID
	}
	return uuid.NewSHA1(EspacioTransaccion, []byte(g.clave))
}

func resolverNombres(ctx context.Context, tx transaccionDeInteligencia, grupos []grupo) (map[string]string, error) {
	ids := make(map[string]struct{})
	for _, g := range grupos {
		for _, apunte := range g.apuntes {
			if usuario, esDeUsuario := UsuarioDeCuenta(apunte.CuentaID); esDeUsuario {
				ids[usuario] = struct{}{}
			}
		}
	}
	nombres := make(map[string]string, len(ids))
	for id := range ids {
		nombre, encontrado, err := nombreDeParte(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if encontrado {
			nombres[id] = nombre
		}
	}
	return nombres, nil
}

const consultaNombreDeParte = `
SELECT COALESCE(p.perfil_nombre, p.nombre)
  FROM auth.personas p
 WHERE p.parte_id = $1::uuid
UNION ALL
SELECT e.razon_social
  FROM auth.empresas e
 WHERE e.parte_id = $1::uuid
 LIMIT 1`

func nombreDeParte(ctx context.Context, tx transaccionDeInteligencia, parteID string) (string, bool, error) {
	if _, err := uuid.Parse(parteID); err != nil {
		return "", false, nil
	}
	var nombre string
	if err := tx.QueryRow(ctx, consultaNombreDeParte, parteID).Scan(&nombre); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("inteligencia: resolver el nombre de la parte: %w", err)
	}
	return nombre, nombre != "", nil
}

func memoriaDeUsuario(ctx context.Context, q *repositorio.Queries, usuarioID string) (Memoria, error) {
	filas, err := q.ListarMemoriaDeUsuario(ctx, usuarioID)
	if err != nil {
		return nil, fmt.Errorf("inteligencia: leer la memoria de %q: %w", usuarioID, err)
	}
	memoria := make(Memoria, len(filas))
	for _, fila := range filas {
		correccion := Correccion{}
		if fila.Categoria.Valid {
			correccion.Categoria = Categoria(fila.Categoria.String)
		}
		if fila.Nombre.Valid {
			correccion.Nombre = fila.Nombre.String
		}
		memoria[ClaveCorreccion{
			Usuario:     fila.UsuarioID,
			TipoDestino: TipoDestino(fila.TipoDestino),
			Nivel:       fila.Nivel,
			Valor:       fila.Valor,
		}] = correccion
	}
	return memoria, nil
}

func escribirMovimiento(ctx context.Context, q *repositorio.Queries, g grupo, efecto Efecto,
	transaccionID uuid.UUID, contraparte Contraparte, memoria Memoria, clasificador *Modelo,
	lexico []ParLexico, resultado *Resultado) error {
	monto, valido := Absoluto(efecto.Neto)
	if !valido {
		return errors.New("inteligencia: el monto del movimiento no cabe en int64")
	}
	direccion := "salida"
	if efecto.Neto > 0 {
		direccion = "entrada"
	}
	moneda := efecto.Moneda
	if moneda == "" {
		moneda = "COP"
	}
	canal := CanalQR
	if contraparte.Tipo == DestinoPersona {
		canal = CanalLlave
	}
	destino := Destino{
		Tipo:   contraparte.Tipo,
		Llave:  contraparte.Llave,
		MID:    contraparte.MID,
		Nombre: contraparte.Nombre,
	}
	entrada := Entrada{
		Canal:            canal,
		Llave:            contraparte.Llave,
		Nombre:           contraparte.Nombre,
		MID:              contraparte.MID,
		TipoDestino:      contraparte.Tipo,
		CategoriaInterna: string(contraparte.CategoriaInterna),
	}
	decision := Decidir(entrada, Recursos{
		Memoria:      Proyectar(memoria, efecto.UsuarioID, destino),
		Lexico:       lexico,
		Clasificador: clasificador,
	}, escalonesDelLote(clasificador, lexico))
	categoria := decision.Categoria
	if categoria == "" {
		categoria = SinCategoria
	}
	origen := decision.Origen
	if origen == "" {
		origen = OrigenSinCategoria
	}
	versionModelo := VersionModelo
	if origen == OrigenTFIDF && clasificador != nil {
		versionModelo = clasificador.VersionModelo()
	}
	comercioNombre := ""
	nombreVisible := contraparte.Texto
	if contraparte.Tipo == DestinoComercio {
		if corregido, encontrado := NombreDe(memoria, efecto.UsuarioID, destino); encontrado {
			nombreVisible = corregido
		}
		comercioNombre = nombreVisible
	}
	destinoPersistido := PersistirDestino(efecto.UsuarioID, destino)
	descripcion := efecto.Descripcion
	if descripcion == "" {
		descripcion = g.descripcion
	}
	parametros := repositorio.InsertarMovimientoEnriquecidoParams{
		UsuarioID:                    efecto.UsuarioID,
		TransaccionID:                pgtype.UUID{Bytes: transaccionID, Valid: true},
		EntryAncla:                   g.entryAncla,
		OcurridoEn:                   pgtype.Timestamptz{Time: g.ocurridoEn, Valid: true},
		Moneda:                       moneda,
		Direccion:                    direccion,
		MontoCentavos:                monto,
		Contraparte:                  nombreVisible,
		ContraparteResuelta:          contraparte.Resuelta,
		Descripcion:                  pgtype.Text{String: descripcion, Valid: descripcion != ""},
		Categoria:                    string(categoria),
		OrigenCategoria:              origen,
		ComercioMid:                  pgtype.Text{String: destinoPersistido.MID, Valid: destinoPersistido.MID != ""},
		ComercioNombre:               pgtype.Text{String: comercioNombre, Valid: comercioNombre != ""},
		Mcc:                          pgtype.Text{},
		VersionModelo:                versionModelo,
		TipoDestino:                  pgtype.Text{String: string(destinoPersistido.Tipo), Valid: destinoPersistido.Tipo != ""},
		ContraparteLlave:             pgtype.Text{String: destinoPersistido.Llave, Valid: destinoPersistido.Llave != ""},
		ContraparteNombreNormalizado: pgtype.Text{String: destinoPersistido.NombreNormalizado, Valid: destinoPersistido.NombreNormalizado != ""},
	}
	if err := q.InsertarMovimientoEnriquecido(ctx, parametros); err != nil {
		return fmt.Errorf("inteligencia: escribir el movimiento %s de %q: %w", transaccionID, efecto.UsuarioID, err)
	}
	resultado.Movimientos++
	resultado.PorOrigen[origen]++
	if !contraparte.Resuelta {
		resultado.SinContraparte++
	}
	if categoria == SinCategoria {
		resultado.SinCategoria++
	}
	if decision.PendienteLLM {
		resultado.Abstenciones++
	}
	return nil
}

// versionDelClasificador es la etiqueta con la que viajan las abstenciones. Sin modelo cargado se
// declara explícitamente, para que un cero no se lea como ausencia de abstenciones.
func (s *Servicio) versionDelClasificador() string {
	if s.clasificador == nil {
		return "sin_modelo"
	}
	return s.clasificador.VersionModelo()
}

func intentarBloqueo(ctx context.Context, tx transaccionDeInteligencia) (bool, error) {
	var bloqueado bool
	if err := tx.QueryRow(ctx,
		"SELECT pg_try_advisory_xact_lock(hashtext('inteligencia.enriquecimiento')::bigint)").Scan(&bloqueado); err != nil {
		return false, fmt.Errorf("inteligencia: tomar el candado del lote: %w", err)
	}
	return bloqueado, nil
}

// escalonesDelLote es el punto único donde el lote decide qué escalones entran. El léxico va antes que
// el clasificador, como manda el contrato, y ninguno de los dos cambia el primer corte por sí solo.
func escalonesDelLote(clasificador *Modelo, lexico []ParLexico) Escalones {
	escalones := PrimerCorte()
	if len(lexico) > 0 {
		escalones[EscalonLexico] = struct{}{}
	}
	if clasificador != nil {
		escalones[EscalonClasificador] = struct{}{}
	}
	return escalones
}
