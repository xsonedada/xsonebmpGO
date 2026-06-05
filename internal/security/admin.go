package security

import (
    "net/http"
    "time"
    
    "github.com/gin-gonic/gin"
    "github.com/gorilla/sessions"
)

// AdminAuthRequired проверяет что пользователь - админ (ID=1)
func AdminAuthRequired(store *sessions.CookieStore) gin.HandlerFunc {
    return func(c *gin.Context) {
        session, _ := store.Get(c.Request, "xsonebmp-session")
        
        adminID, ok := session.Values["admin_id"].(int)
        if !ok || adminID != 1 {
            // Логируем попытку доступа
            SecurityLogger.Critical("Unauthorized admin access attempt",
                "ip", c.ClientIP(),
                "path", c.Request.URL.Path)
            
            c.Redirect(http.StatusFound, "/admin/login")
            c.Abort()
            return
        }
        
        // Обновляем время последней активности
        session.Values["last_activity"] = time.Now()
        session.Save(c.Request, c.Writer)
        
        c.Next()
    }
}

// AdminIPWhitelist опционально: разрешить админку только с определенных IP
func AdminIPWhitelist(allowedIPs []string) gin.HandlerFunc {
    return func(c *gin.Context) {
        clientIP := c.ClientIP()
        
        for _, allowedIP := range allowedIPs {
            if clientIP == allowedIP {
                c.Next()
                return
            }
        }
        
        SecurityLogger.Critical("Admin access from non-whitelisted IP",
            "ip", clientIP)
        
        c.JSON(http.StatusForbidden, gin.H{
            "error": "Access denied",
        })
        c.Abort()
    }
}