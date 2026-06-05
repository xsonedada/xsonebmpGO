// static/js/vibration.js — Тактильная обратная связь XSoneBMP
(function() {
    'use strict';
    
    var isIOS = /iPad|iPhone|iPod/.test(navigator.userAgent);
    
    // Для Android — настоящая вибрация
    function androidVibrate(pattern) {
        if (navigator.vibrate) {
            navigator.vibrate(pattern);
        }
    }
    
    // Для iOS — визуальный тактильный отклик
    function iosHaptic(type) {
        var dot = document.createElement('div');
        dot.style.cssText = 'position:fixed;top:50%;left:50%;width:4px;height:4px;border-radius:50%;background:rgba(139,92,246,0.6);pointer-events:none;z-index:99999;transform:translate(-50%,-50%) scale(0);transition:all 0.3s ease;';
        document.body.appendChild(dot);
        
        var scale = type === 'heavy' ? 80 : type === 'medium' ? 40 : 20;
        var duration = type === 'heavy' ? 200 : type === 'medium' ? 150 : 100;
        
        requestAnimationFrame(function() {
            dot.style.transform = 'translate(-50%,-50%) scale(' + scale + ')';
            dot.style.opacity = '0';
            dot.style.transitionDuration = duration + 'ms';
        });
        
        setTimeout(function() {
            dot.remove();
        }, duration + 50);
    }
    
    function vibrate(type, pattern) {
        if (isIOS) {
            var intensity = 'light';
            if (type === 'newOrder' || type === 'error') intensity = 'heavy';
            else if (type === 'payment' || type === 'achievement') intensity = 'medium';
            iosHaptic(intensity);
        } else {
            androidVibrate(pattern);
        }
    }
    
    var patterns = {
        newOrder: [100, 50, 100, 50, 200],
        newMessage: [50, 100, 50],
        payment: [30, 30, 30, 30, 30],
        completed: [80, 40, 80],
        error: [200, 100, 200],
        confirm: [30],
        notification: [60, 30, 60],
        login: [40, 20, 80],
        qrLogin: [50, 50, 100],
        achievement: [20, 15, 20, 15, 80],
        topUp: [40, 25, 40, 25, 100],
        review: [30, 20, 30, 20, 30]
    };
    
    window.vibrate = function(type) {
        var pattern = patterns[type] || patterns.notification;
        vibrate(type, pattern);
    };
})();