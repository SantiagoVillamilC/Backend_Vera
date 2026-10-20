// Comando evaluar-clasificador mide el clasificador Go contra un conjunto de oro etiquetado por
// personas. No entrena nada, no ajusta nada y no escribe en la copia: solo lee la hoja y cuenta.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
)

// Los modos que se pueden medir. El catálogo entra por `-catalogo` y no necesita Postgres: se mide el
// CSV del artefacto. Memoria, cuenta interna y MCC sí viven en Postgres y no se miden con este comando.
const (
	ModoLexico       = "lexico"
	ModoClasificador = "clasificador"
	ModoCatalogo     = "catalogo"
	ModoPrimerCorte  = "primer-corte"
	ModoTodos        = "todos"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "evaluar-clasificador:", err)
		os.Exit(1)
	}
}

func run() error {
	hoja := flag.String("csv", "", "hoja con id_oro, nombre_qr, categoria y, opcional, parece_persona y mcc")
	conjunto := flag.String("conjunto", "", "CSV con id_oro, parte y estrato para agrupar el informe")
	parte := flag.String("parte", "", "parte del conjunto que se mide, vacío para todas")
	modo := flag.String("modo", ModoPrimerCorte, "lexico, clasificador, catalogo, primer-corte o todos")
	umbral := flag.Float64("umbral", 0, "precisión mínima de lo mostrado, 0 para solo informar")
	salida := flag.String("salida", "", "ruta donde escribir el informe JSON, vacío para solo stdout")
	lexicoCSV := flag.String("lexico", "", "CSV con palabra y categoría que reemplaza el léxico del artefacto")
	catalogoCSV := flag.String("catalogo", "", "CSV con nombre_normalizado y categoría del catálogo a medir")
	flag.Parse()

	if *hoja == "" {
		return errors.New("falta -csv con la hoja del conjunto de oro")
	}
	if _, err := modoDe(*modo); err != nil {
		return err
	}
	if *modo == ModoCatalogo && *catalogoCSV == "" {
		return errors.New("el modo catalogo necesita -catalogo con el CSV del catálogo")
	}
	if *catalogoCSV != "" && *modo != ModoCatalogo && *modo != ModoTodos {
		return fmt.Errorf("el modo %s no enciende el catálogo: usa catalogo o todos", *modo)
	}
	filas, crudo, err := leerHoja(*hoja)
	if err != nil {
		return err
	}
	if *conjunto != "" {
		if err := completarPartes(*conjunto, filas); err != nil {
			return err
		}
	}
	if *parte != "" {
		filas, err = filtrarParte(filas, *parte)
		if err != nil {
			return err
		}
	}
	suma := sha256.Sum256(crudo)

	modelo, err := inteligencia.ModeloTFIDFEmbebido()
	if err != nil {
		return fmt.Errorf("cargar el artefacto: %w", err)
	}
	lexico := paresDelLexico(modelo)
	var lexicoSuma string
	if *lexicoCSV != "" {
		lexico, lexicoSuma, err = leerLexico(*lexicoCSV)
		if err != nil {
			return err
		}
	}
	var catalogo map[string]inteligencia.Categoria
	var catalogoSuma string
	if *catalogoCSV != "" {
		catalogo, catalogoSuma, err = leerCatalogo(*catalogoCSV)
		if err != nil {
			return err
		}
	}
	decidir, err := decisorDe(*modo, modelo, lexico, catalogo)
	if err != nil {
		return err
	}
	informe, err := Evaluar(*modo, filas, decidir, *umbral)
	if err != nil {
		return err
	}
	informe.CSV = *hoja
	informe.CSVSha256 = hex.EncodeToString(suma[:])
	informe.Parte = *parte
	informe.Artefacto = ArtefactoMedido{
		Version: modelo.VersionModelo(),
		Huella:  inteligencia.HuellaArtefactoTFIDF,
		Sha256:  inteligencia.HuellaArtefactoTFIDF,
	}
	if catalogo != nil {
		informe.CatalogoCSV = *catalogoCSV
		informe.CatalogoSha256 = catalogoSuma
	}
	if *lexicoCSV != "" {
		informe.LexicoCSV = *lexicoCSV
		informe.LexicoSha256 = lexicoSuma
	}
	crudoInforme, err := json.MarshalIndent(informe, "", "  ")
	if err != nil {
		return fmt.Errorf("serializar el informe: %w", err)
	}
	fmt.Println(string(crudoInforme))
	if *salida != "" {
		if err := os.WriteFile(*salida, crudoInforme, 0o644); err != nil { //nolint:gosec // la ruta la pasa el operador.
			return fmt.Errorf("escribir el informe: %w", err)
		}
	}
	resumenEnStderr(informe)
	if *umbral > 0 && !informe.Cumple {
		os.Exit(2)
	}
	return nil
}

// modoDe valida el modo pedido.
func modoDe(modo string) (string, error) {
	for _, valido := range []string{ModoLexico, ModoClasificador, ModoCatalogo, ModoPrimerCorte, ModoTodos} {
		if modo == valido {
			return modo, nil
		}
	}
	return "", fmt.Errorf("modo %q inválido: usa %s, %s, %s, %s o %s", modo, ModoLexico, ModoClasificador,
		ModoCatalogo, ModoPrimerCorte, ModoTodos)
}

// decisorDe arma el decisor del modo con los escalones que no necesitan Postgres.
func decisorDe(modo string, modelo *inteligencia.Modelo, lexico []inteligencia.ParLexico,
	catalogo map[string]inteligencia.Categoria) (Decisor, error) {
	escalones, err := escalonesDe(modo)
	if err != nil {
		return nil, err
	}
	recursos := inteligencia.Recursos{Lexico: lexico, CatalogoNombres: catalogo}
	if modo == ModoClasificador || modo == ModoPrimerCorte || modo == ModoTodos {
		recursos.Clasificador = modelo
	}
	return func(fila Fila) DecisionDeOro {
		decision := inteligencia.Decidir(inteligencia.Entrada{
			Canal:       inteligencia.CanalQR,
			Nombre:      fila.Nombre,
			MCC:         fila.MCC,
			TipoDestino: inteligencia.DestinoComercio,
		}, recursos, escalones)
		return DecisionDeOro{
			Categoria: string(decision.Categoria),
			Origen:    decision.Origen,
			Abstuvo:   decision.PendienteLLM,
			Lexico:    coincidenciasDelLexico(escalones, lexico, fila.Nombre),
		}
	}, nil
}

// coincidenciasDelLexico devuelve nil cuando el modo no enciende el escalón 5 y una lista no nula,
// aunque esté vacía, cuando sí lo enciende. Así el informe distingue «no se midió» de «no hay palabra».
func coincidenciasDelLexico(escalones inteligencia.Escalones, lexico []inteligencia.ParLexico,
	nombre string) []Coincidencia {
	if !escalones.Contiene(inteligencia.EscalonLexico) {
		return nil
	}
	pares := inteligencia.CoincidenciasDelLexico(lexico, nombre)
	coincidencias := make([]Coincidencia, 0, len(pares))
	for _, par := range pares {
		coincidencias = append(coincidencias, Coincidencia{Palabra: par.Palabra, Categoria: string(par.Categoria)})
	}
	return coincidencias
}

// escalonesDe traduce el modo al conjunto de escalones del contrato.
func escalonesDe(modo string) (inteligencia.Escalones, error) {
	switch modo {
	case ModoLexico:
		return inteligencia.Escalones{inteligencia.EscalonLexico: {}}, nil
	case ModoClasificador:
		return inteligencia.Escalones{inteligencia.EscalonClasificador: {}}, nil
	case ModoCatalogo:
		return inteligencia.Escalones{inteligencia.EscalonCatalogo: {}}, nil
	case ModoPrimerCorte:
		// Primer corte tal como queda con las dos banderas encendidas: léxico antes que clasificador.
		escalones := inteligencia.PrimerCorte()
		escalones[inteligencia.EscalonLexico] = struct{}{}
		escalones[inteligencia.EscalonClasificador] = struct{}{}
		return escalones, nil
	case ModoTodos:
		return inteligencia.TodosLosEscalones(), nil
	default:
		return nil, fmt.Errorf("modo %q inválido", modo)
	}
}

// paresDelLexico convierte el léxico del artefacto en el formato del contrato, en orden estable.
func paresDelLexico(modelo *inteligencia.Modelo) []inteligencia.ParLexico {
	palabras := make([]string, 0, len(modelo.Lexico))
	for palabra := range modelo.Lexico {
		palabras = append(palabras, palabra)
	}
	sort.Strings(palabras)
	pares := make([]inteligencia.ParLexico, 0, len(palabras))
	for _, palabra := range palabras {
		pares = append(pares, inteligencia.ParLexico{Palabra: palabra, Categoria: modelo.Lexico[palabra]})
	}
	return pares
}

// leerHoja lee el CSV y devuelve el crudo para hashearlo.
func leerHoja(ruta string) ([]Fila, []byte, error) {
	crudo, err := os.ReadFile(ruta) //nolint:gosec // la ruta la pasa el operador.
	if err != nil {
		return nil, nil, fmt.Errorf("leer la hoja: %w", err)
	}
	cabecera, registros, err := leerFilas(crudo)
	if err != nil {
		return nil, nil, err
	}
	indice := indiceDeColumnas(cabecera)
	for _, obligatoria := range []string{"id_oro", "categoria"} {
		if _, existe := indice[obligatoria]; !existe {
			return nil, nil, fmt.Errorf("la hoja no trae la columna %q", obligatoria)
		}
	}
	if _, existe := indice["nombre_qr"]; !existe {
		if _, alternativa := indice["razon_social"]; !alternativa {
			return nil, nil, errors.New("la hoja no trae nombre_qr ni razon_social")
		}
	}
	filas := make([]Fila, 0, len(registros))
	for numero, registro := range registros {
		fila := Fila{
			ID:            campo(registro, indice, "id_oro"),
			Nombre:        campo(registro, indice, "nombre_qr"),
			Oro:           campo(registro, indice, "categoria"),
			ParecePersona: esSi(campo(registro, indice, "parece_persona")),
			MCC:           campo(registro, indice, "mcc"),
		}
		if fila.Nombre == "" {
			fila.Nombre = campo(registro, indice, "razon_social")
		}
		if fila.ID == "" && fila.Nombre == "" && fila.Oro == "" {
			continue
		}
		if fila.ID == "" || fila.Nombre == "" {
			return nil, nil, fmt.Errorf("la fila %d no trae identificador o nombre", numero+2)
		}
		filas = append(filas, fila)
	}
	return filas, crudo, nil
}

// completarPartes cruza la hoja con el conjunto para agrupar por parte y por estrato.
func completarPartes(ruta string, filas []Fila) error {
	crudo, err := os.ReadFile(ruta) //nolint:gosec // la ruta la pasa el operador.
	if err != nil {
		return fmt.Errorf("leer el conjunto: %w", err)
	}
	cabecera, registros, err := leerFilas(crudo)
	if err != nil {
		return err
	}
	indice := indiceDeColumnas(cabecera)
	partes := map[string][2]string{}
	for _, registro := range registros {
		id := campo(registro, indice, "id_oro")
		if id == "" {
			continue
		}
		partes[id] = [2]string{
			campo(registro, indice, "parte"),
			campo(registro, indice, "estrato"),
		}
	}
	faltantes := 0
	for i := range filas {
		valores, existe := partes[filas[i].ID]
		if !existe {
			faltantes++
			continue
		}
		filas[i].Parte = valores[0]
		filas[i].Estrato = valores[1]
	}
	if faltantes == len(filas) {
		return errors.New("el conjunto no comparte ningún id_oro con la hoja")
	}
	return nil
}

// filtrarParte deja solo las filas de la parte pedida y falla si no queda ninguna, para que un
// nombre de parte mal escrito no se reporte como una medición vacía.
func filtrarParte(filas []Fila, parte string) ([]Fila, error) {
	filtradas := make([]Fila, 0, len(filas))
	for _, fila := range filas {
		if fila.Parte == parte {
			filtradas = append(filtradas, fila)
		}
	}
	if len(filtradas) == 0 {
		return nil, fmt.Errorf("la parte %q no tiene filas: usa -conjunto con una parte del conjunto", parte)
	}
	return filtradas, nil
}

// resumenEnStderr imprime la línea que se lee en la terminal, sin ensuciar el JSON de stdout.
func resumenEnStderr(informe Informe) {
	fmt.Fprintf(os.Stderr,
		"modo %s: %d filas, %d mostradas, %d correctas, precisión %.3f (Wilson %.3f a %.3f), cobertura %.3f, %d abstenciones, %d sin decisión\n",
		informe.Modo, informe.Total.Filas, informe.Total.Mostradas, informe.Total.Correctas,
		informe.Total.Precision, informe.Total.WilsonBajo, informe.Total.WilsonAlto,
		informe.Total.Cobertura, informe.Total.Abstenciones, informe.Total.SinDecision)
	if informe.Umbral > 0 {
		estado := "no cumple"
		if informe.Cumple {
			estado = "cumple"
		}
		fmt.Fprintf(os.Stderr, "umbral %.2f: %s\n", informe.Umbral, estado)
	}
	if informe.Lexico != nil {
		fmt.Fprintf(os.Stderr, "léxico: %d con una categoría (%d correctas), %d en conflicto (%d con acierto), %d sin palabra\n",
			informe.Lexico.UnaCategoria, informe.Lexico.UnaCategoriaCorrecta, informe.Lexico.Conflictos,
			informe.Lexico.ConflictosConAcierto, informe.Lexico.SinPalabra)
	}
}
