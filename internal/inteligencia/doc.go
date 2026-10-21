// Package inteligencia mantiene la copia de lectura enriquecida del ledger, la memoria privada de
// correcciones y la vista de consumo del usuario. No escribe en el ledger: el rol con el que
// consulta solo tiene SELECT sobre nucleo, y toda escritura vive en su propio esquema.
package inteligencia
