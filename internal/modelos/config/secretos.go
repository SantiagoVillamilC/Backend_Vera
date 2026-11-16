package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

const longitudMinimaToken = 32

// leerArchivoPrivado lee un archivo cuyo modo no da acceso a grupo ni a otros. Los mensajes de error
// nunca incluyen el contenido.
func leerArchivoPrivado(ruta string) ([]byte, error) {
	info, err := os.Stat(ruta)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer %s: %w", ruta, errors.Unwrap(err))
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s debe ser un archivo regular con permisos 0600 o mas estrictos", ruta)
	}
	datos, err := os.ReadFile(ruta) //nolint:gosec // la ruta viene de la configuracion del operador y ya se valido el modo
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer %s: %w", ruta, errors.Unwrap(err))
	}
	return datos, nil
}

func parsearEnv(datos []byte) map[string]string {
	valores := map[string]string{}
	for _, linea := range strings.Split(string(datos), "\n") {
		linea = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(linea), "export "))
		if linea == "" || strings.HasPrefix(linea, "#") {
			continue
		}
		clave, valor, ok := strings.Cut(linea, "=")
		if !ok {
			continue
		}
		valor = strings.TrimSpace(valor)
		if len(valor) >= 2 && (valor[0] == '"' || valor[0] == '\'') && valor[len(valor)-1] == valor[0] {
			valor = valor[1 : len(valor)-1]
		}
		valores[strings.TrimSpace(clave)] = valor
	}
	return valores
}

// LeerClaveRunPod lee RUNPOD_API_KEY del archivo de entorno (solo root). Con ruta vacía usa la
// variable de entorno del proceso, pensada para un EnvironmentFile de systemd.
func LeerClaveRunPod(ruta string, buscar func(string) (string, bool)) (string, error) {
	if ruta == "" {
		if v, ok := buscar("RUNPOD_API_KEY"); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), nil
		}
		return "", errors.New("falta RUNPOD_API_KEY")
	}
	datos, err := leerArchivoPrivado(ruta)
	if err != nil {
		return "", err
	}
	clave := parsearEnv(datos)["RUNPOD_API_KEY"]
	if clave == "" {
		return "", fmt.Errorf("%s no define RUNPOD_API_KEY", ruta)
	}
	return clave, nil
}

// LeerTokensModelos lee VERA_STT_TOKEN y VERA_VOZ_TOKEN (32 caracteres o más cada uno).
func LeerTokensModelos(ruta string) (stt, voz string, err error) {
	datos, err := leerArchivoPrivado(ruta)
	if err != nil {
		return "", "", err
	}
	valores := parsearEnv(datos)
	stt, voz = valores["VERA_STT_TOKEN"], valores["VERA_VOZ_TOKEN"]
	if len(stt) < longitudMinimaToken || len(voz) < longitudMinimaToken {
		return "", "", fmt.Errorf("%s: VERA_STT_TOKEN y VERA_VOZ_TOKEN deben tener %d caracteres o mas", ruta, longitudMinimaToken)
	}
	return stt, voz, nil
}

// LeerTokenControl lee el token Bearer de la API de control (archivo con solo el token).
func LeerTokenControl(ruta string) (string, error) {
	datos, err := leerArchivoPrivado(ruta)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(datos))
	if len(token) < longitudMinimaToken {
		return "", fmt.Errorf("%s: el token de control debe tener %d caracteres o mas", ruta, longitudMinimaToken)
	}
	return token, nil
}

// LeerLlavePublica lee la llave pública SSH que se inyecta en el pod como PUBLIC_KEY.
func LeerLlavePublica(ruta string) (string, error) {
	datos, err := os.ReadFile(ruta) //nolint:gosec // la ruta viene de la configuracion del operador
	if err != nil {
		return "", fmt.Errorf("no se pudo leer la llave publica %s", ruta)
	}
	llave := strings.TrimSpace(string(datos))
	if !strings.HasPrefix(llave, "ssh-") || strings.ContainsAny(llave, "\r\n") {
		return "", fmt.Errorf("%s no parece una llave publica ssh de una sola linea", ruta)
	}
	return llave, nil
}
