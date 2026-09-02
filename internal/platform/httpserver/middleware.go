package httpserver

import("log";"net/http";"strings")
func Auth(jwtSecret string,next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){if r.URL.Path=="/login"||r.URL.Path=="/register"{next.ServeHTTP(w,r);return};token:=strings.TrimPrefix(r.Header.Get("Authorization"),"Bearer ");if jwtSecret==""||token==""{http.Error(w,"unauthorized",http.StatusUnauthorized);return};next.ServeHTTP(w,r)})}
func Logging(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){log.Printf("%s %s",r.Method,r.URL.Path);next.ServeHTTP(w,r)})}
