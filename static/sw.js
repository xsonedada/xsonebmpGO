// XSoneBMP Service Worker v3
const CACHE = 'xsonebmp-v3';
const OFFLINE = '/offline';

const PRE_CACHE = [
  '/',
  '/marketplace',
  '/about',
  '/contact',
  '/offline',
  '/static/css/style.css',
  '/static/css/ai-assistant.css',
  '/static/js/app.js',
  '/static/js/ai-assistant.js',
  '/static/js/audio/engine.js',
  '/static/manifest.json',
  'https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.5.1/css/all.min.css',
  'https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800;900&display=swap'
];

// Install
self.addEventListener('install', e => {
  e.waitUntil(
    caches.open(CACHE).then(cache => cache.addAll(PRE_CACHE).catch(() => {})).then(() => self.skipWaiting())
  );
});

// Activate
self.addEventListener('activate', e => {
  e.waitUntil(
    caches.keys().then(keys => Promise.all(keys.filter(k => k !== CACHE).map(k => caches.delete(k)))).then(() => self.clients.claim())
  );
});

// Fetch (Network First)
self.addEventListener('fetch', e => {
  if (e.request.method !== 'GET') return;
  if (e.request.url.includes('/api/')) return;
  if (e.request.url.includes('/admin/')) return;
  
  e.respondWith(
    fetch(e.request).then(res => {
      if (res.status === 200) {
        const clone = res.clone();
        caches.open(CACHE).then(cache => cache.put(e.request, clone));
      }
      return res;
    }).catch(() => caches.match(e.request).then(cached => {
      if (cached) return cached;
      if (e.request.mode === 'navigate') return caches.match(OFFLINE);
      return new Response('', { status: 408 });
    }))
  );
});

// Push
self.addEventListener('push', e => {
  if (!e.data) return;
  const d = e.data.json();
  e.waitUntil(
    self.registration.showNotification(d.title || 'XSoneBMP', {
      body: d.body || '',
      icon: '/static/icons/icon-192.png',
      badge: '/static/icons/badge-72.png',
      data: { url: d.url || '/' },
      vibrate: [100, 50, 100],
      tag: d.tag || 'default'
    })
  );
});

// Notification click
self.addEventListener('notificationclick', e => {
  e.notification.close();
  e.waitUntil(
    clients.matchAll({ type: 'window' }).then(clients => {
      const url = e.notification.data.url || '/';
      for (const c of clients) {
        if (c.url.includes(url) && 'focus' in c) return c.focus();
      }
      return clients.openWindow(url);
    })
  );
});