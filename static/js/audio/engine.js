// static/js/audio/engine.js — XSoneBMP
const AudioEngine = (function() {
  let ctx = null;
  let masterGain = null;
  let isMuted = false;
  let volume = 0.35;

  function init() {
    if (ctx) return;
    try {
      ctx = new (window.AudioContext || window.webkitAudioContext)();
    } catch(e) {
      return;
    }
    masterGain = ctx.createGain();
    masterGain.gain.value = volume;
    masterGain.connect(ctx.destination);
  }

  function resume() {
    init();
    if (ctx && ctx.state === 'suspended') {
      ctx.resume();
    }
  }

  function bassTone(freq, duration, vol) {
    init();
    if (!ctx || isMuted) return;
    resume();

    const now = ctx.currentTime;

    const osc = ctx.createOscillator();
    const gain = ctx.createGain();
    osc.type = 'sine';
    osc.frequency.value = freq;
    gain.gain.setValueAtTime(0, now);
    gain.gain.linearRampToValueAtTime(vol * 0.35, now + 0.03);
    gain.gain.setValueAtTime(vol * 0.35, now + duration * 0.3);
    gain.gain.exponentialRampToValueAtTime(0.001, now + duration);
    osc.connect(gain);
    gain.connect(masterGain);
    osc.start(now);
    osc.stop(now + duration + 0.03);

    const sub = ctx.createOscillator();
    const subGain = ctx.createGain();
    sub.type = 'sine';
    sub.frequency.value = freq / 2;
    subGain.gain.setValueAtTime(0, now);
    subGain.gain.linearRampToValueAtTime(vol * 0.08, now + 0.04);
    subGain.gain.exponentialRampToValueAtTime(0.001, now + duration * 1.2);
    sub.connect(subGain);
    subGain.connect(masterGain);
    sub.start(now);
    sub.stop(now + duration * 1.2 + 0.03);
  }

  function twoBassTone(f1, f2, dur, gap, vol) {
    init();
    if (!ctx || isMuted) return;
    resume();
    const now = ctx.currentTime;
    bassToneAt(f1, dur, vol, now);
    bassToneAt(f2, dur, vol * 0.8, now + gap);
  }

  function bassToneAt(freq, duration, vol, startTime) {
    const osc = ctx.createOscillator();
    const gain = ctx.createGain();
    osc.type = 'sine';
    osc.frequency.value = freq;
    gain.gain.setValueAtTime(0, startTime);
    gain.gain.linearRampToValueAtTime(vol * 0.3, startTime + 0.025);
    gain.gain.setValueAtTime(vol * 0.3, startTime + duration * 0.3);
    gain.gain.exponentialRampToValueAtTime(0.001, startTime + duration);
    osc.connect(gain);
    gain.connect(masterGain);
    osc.start(startTime);
    osc.stop(startTime + duration + 0.03);

    const sub = ctx.createOscillator();
    const subGain = ctx.createGain();
    sub.type = 'sine';
    sub.frequency.value = freq / 2;
    subGain.gain.setValueAtTime(0, startTime);
    subGain.gain.linearRampToValueAtTime(vol * 0.07, startTime + 0.04);
    subGain.gain.exponentialRampToValueAtTime(0.001, startTime + duration * 1.3);
    sub.connect(subGain);
    subGain.connect(masterGain);
    sub.start(startTime);
    sub.stop(startTime + duration * 1.3 + 0.03);
  }

  return {
    success: function() {
      twoBassTone(160, 200, 0.25, 0.14, 0.38);
    },
    
    error: function() {
      bassTone(100, 0.55, 0.28);
    },
    
    warning: function() {
      twoBassTone(180, 130, 0.22, 0.1, 0.32);
    },
    
    mute: function() { isMuted = true; },
    unmute: function() { isMuted = false; },
    isMuted: function() { return isMuted; }
  };
})();