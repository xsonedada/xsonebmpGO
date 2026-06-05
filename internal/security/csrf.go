package security

import (
    "crypto/rand"
    "encoding/base64"
    "net/http"
    
    "github.com/gin-gonic/gin"
    "github.com/gorilla/sessions"
)

type CSRFProtection struct {
    Store *sessions.CookieStore
}

func NewCSRFProtection(store *sessions.CookieStore) *CSRFProtection {
    return &CSRFProtection{Store: store}
}

// GenerateToken создает новый CSRF токен
func (c *CSRFProtection) GenerateToken(ctx *gin.Context) string {
    b := make([]byte, 32)
    rand.Read(b)
    token := base64.StdEncoding.EncodeToString(b)
    
    session, _ := c.Store.Get(ctx.Request, "xsonebmp-session")
    session.Values["csrf_token"] = token
    session.Save(ctx.Request, ctx.Writer)
    
    return token
}

// Middleware проверяет CSRF токен для всех изменяющих методов
func (c *CSRFProtection) Middleware() gin.HandlerFunc {
    return func(ctx *gin.Context) {
        // Пропускаем безопасные методы
        if ctx.Request.Method == "GET" || 
           ctx.Request.Method == "HEAD" || 
           ctx.Request.Method == "OPTIONS" {
            ctx.Next()
            return
        }
        
        var token string
        
        // Для AJAX запросов проверяем заголовок
        if ctx.GetHeader("X-Requested-With") == "XMLHttpRequest" {
            token = ctx.GetHeader("X-CSRF-Token")
        } else {
            // Для обычных форм - из POST параметра
            token = ctx.PostForm("csrf_token")
        }
        
        session, _ := c.Store.Get(ctx.Request, "xsonebmp-session")
        storedToken, ok := session.Values["csrf_token"].(string)
        
        if !ok || token == "" || token != storedToken {
            SecurityLogger.Warn("CSRF attack blocked", 
                "ip", ctx.ClientIP(),
                "method", ctx.Request.Method,
                "path", ctx.Request.URL.Path)
            
            ctx.JSON(http.StatusForbidden, gin.H{
                "error": "CSRF token invalid",
            })
            ctx.Abort()
            return
        }
        
        ctx.Next()
    }
}