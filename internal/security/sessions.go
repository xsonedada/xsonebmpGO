package security

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/sessions"
)

var SessionStore *sessions.CookieStore

func InitSessions() *sessions.CookieStore {
	key := []byte("12345678901234567890123456789012")
	
	store := sessions.NewCookieStore(key)
	
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7,
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	}
	
	SessionStore = store
	return store
}

func RegenerateSession(ctx *gin.Context, userID int, username string) error {
	session, _ := SessionStore.Get(ctx.Request, "xsonebmp-session")
	
	// Очищаем старые данные
	session.Values = make(map[interface{}]interface{})
	
	// Используем ТОЛЬКО простые типы: int, string
	session.Values["user_id"] = userID
	session.Values["username"] = username
	
	// Сохраняем
	return session.Save(ctx.Request, ctx.Writer)
}

func ValidateSession(ctx *gin.Context) (int, bool) {
	session, _ := SessionStore.Get(ctx.Request, "xsonebmp-session")
	
	userID, ok := session.Values["user_id"].(int)
	if !ok {
		return 0, false
	}
	
	return userID, true
}

func ClearSession(ctx *gin.Context) error {
	session, _ := SessionStore.Get(ctx.Request, "xsonebmp-session")
	session.Values = make(map[interface{}]interface{})
	session.Options.MaxAge = -1
	return session.Save(ctx.Request, ctx.Writer)
}