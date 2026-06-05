// static/js/audio/synth.js
import { audioEngine } from './engine.js';

class Synth {
  constructor() {
    this.ctx = audioEngine.ctx;
  }

  // ========== СУБТРАКТИВНЫЙ СИНТЕЗАТОР ==========
  // Основа для большинства "приятных" звуков
  
  /**
   * Мягкий пэд (звук-атмосфера)
   */
  generateCosmicPad(duration = 2.0) {
    const ctx = this.ctx;
    const sampleRate = ctx.sampleRate;
    const length = Math.floor(sampleRate * duration);
    const buffer = ctx.createBuffer(1, length, sampleRate);
    const data = buffer.getChannelData(0);

    // Многослойный осциллятор
    for (let i = 0; i < length; i++) {
      const t = i / sampleRate;
      const envelope = Math.exp(-t * 0.8) * (1 - Math.exp(-t * 30));
      
      // Основной тон + квинта + октава (слегка расстроенные)
      const freq = 220; // A3
      const fundamental = Math.sin(2 * Math.PI * freq * t);
      const fifth = Math.sin(2 * Math.PI * (freq * 1.498) * t) * 0.5;
      const octave = Math.sin(2 * Math.PI * (freq * 2.003) * t) * 0.3;
      
      // Лёгкое ЧМ-вибрато для "космического" звучания
      const vibrato = 1 + Math.sin(2 * Math.PI * 5.5 * t) * 0.003;
      
      data[i] = (fundamental + fifth + octave) * envelope * 0.4 * vibrato;
    }

    return buffer;
  }

  /**
   * Сай-фай звук наведения (hover)
   */
  generateHoverSound() {
    const ctx = this.ctx;
    const duration = 0.25;
    const sampleRate = ctx.sampleRate;
    const length = Math.floor(sampleRate * duration);
    const buffer = ctx.createBuffer(1, length, sampleRate);
    const data = buffer.getChannelData(0);

    for (let i = 0; i < length; i++) {
      const t = i / sampleRate;
      const envelope = Math.exp(-t * 8) * (1 - Math.exp(-t * 200));
      
      // Частота растёт от 800 до 2400 Гц (эффект "взлёта")
      const freq = 800 + t * 6400;
      const phase = 2 * Math.PI * freq * t;
      
      // Синус + мягкий треугольник для приятного тембра
      const sine = Math.sin(phase);
      const tri = Math.asin(Math.sin(phase)) * (2 / Math.PI);
      
      data[i] = (sine * 0.6 + tri * 0.4) * envelope * 0.35;
    }

    return buffer;
  }

  /**
   * Звук клика (выбор, активация)
   */
  generateClickSound() {
    const ctx = this.ctx;
    const duration = 0.12;
    const sampleRate = ctx.sampleRate;
    const length = Math.floor(sampleRate * duration);
    const buffer = ctx.createBuffer(1, length, sampleRate);
    const data = buffer.getChannelData(0);

    for (let i = 0; i < length; i++) {
      const t = i / sampleRate;
      const envelope = Math.exp(-t * 40) * (1 - Math.exp(-t * 500));
      
      // Два тона: 1200 Гц и 1800 Гц (приятный интервал - квинта)
      const f1 = Math.sin(2 * Math.PI * 1200 * t);
      const f2 = Math.sin(2 * Math.PI * 1800 * t) * 0.5;
      
      data[i] = (f1 + f2) * envelope * 0.45;
    }

    return buffer;
  }

  /**
   * Подтверждение заказа — тёплый, многослойный "успех"
   */
  generateSuccessSound() {
    const ctx = this.ctx;
    const duration = 0.9;
    const sampleRate = ctx.sampleRate;
    const length = Math.floor(sampleRate * duration);
    const buffer = ctx.createBuffer(1, length, sampleRate);
    const data = buffer.getChannelData(0);

    for (let i = 0; i < length; i++) {
      const t = i / sampleRate;
      const envelope = Math.exp(-t * 2.5) * (1 - Math.exp(-t * 30));
      
      // Восходящее арпеджио: A4 → C#5 → E5 (мажорное трезвучие)
      const note1 = Math.sin(2 * Math.PI * 440 * t) * (t < 0.3 ? 1 : Math.exp(-(t - 0.3) * 8));
      const note2 = Math.sin(2 * Math.PI * 554 * t) * (t > 0.15 && t < 0.5 ? 1 : 0) * Math.exp(-(t - 0.15) * 6);
      const note3 = Math.sin(2 * Math.PI * 659 * t) * (t > 0.3 ? 1 : 0) * Math.exp(-(t - 0.3) * 4);
      
      // Мягкая "звёздная пыль" на высоких частотах
      const sparkle = Math.sin(2 * Math.PI * 3000 * t) * Math.exp(-t * 10) * 0.15;
      
      data[i] = (note1 + note2 + note3 + sparkle) * envelope * 0.5;
    }

    return buffer;
  }

  /**
   * Звук ошибки — мягкий, не раздражающий
   */
  generateErrorSound() {
    const ctx = this.ctx;
    const duration = 0.45;
    const sampleRate = ctx.sampleRate;
    const length = Math.floor(sampleRate * duration);
    const buffer = ctx.createBuffer(1, length, sampleRate);
    const data = buffer.getChannelData(0);

    for (let i = 0; i < length; i++) {
      const t = i / sampleRate;
      const envelope = Math.exp(-t * 6) * (1 - Math.exp(-t * 50));
      
      // Низкая частота + лёгкий диссонанс (малая секунда)
      const f1 = Math.sin(2 * Math.PI * 180 * t);
      const f2 = Math.sin(2 * Math.PI * 190 * t) * 0.3;
      
      // Частотная модуляция для "вибрации"
      const mod = 1 + Math.sin(2 * Math.PI * 12 * t) * 0.02;
      
      data[i] = (f1 * mod + f2) * envelope * 0.4;
    }

    return buffer;
  }

  /**
   * Космический "свип" — для переходов, открытия модалок
   */
  generateSweepSound(direction = 'up') {
    const ctx = this.ctx;
    const duration = 0.6;
    const sampleRate = ctx.sampleRate;
    const length = Math.floor(sampleRate * duration);
    const buffer = ctx.createBuffer(1, length, sampleRate);
    const data = buffer.getChannelData(0);

    const startFreq = direction === 'up' ? 200 : 3000;
    const endFreq = direction === 'up' ? 3000 : 200;

    for (let i = 0; i < length; i++) {
      const t = i / sampleRate;
      const envelope = Math.exp(-t * 3) * (1 - Math.exp(-t * 20));
      const freq = startFreq + (endFreq - startFreq) * (t / duration);
      const phase = 2 * Math.PI * freq * t;
      
      // Фильтрованный шум для текстуры
      const noise = (Math.random() * 2 - 1) * 0.15 * Math.exp(-t * 8);
      
      data[i] = (Math.sin(phase) * 0.7 + noise) * envelope * 0.4;
    }

    return buffer;
  }

  /**
   * Тихая фоновая "музыка" — бесконечный эмбиент
   */
  generateAmbientDrone(baseFreq = 55) {
    const ctx = this.ctx;
    const duration = 10; // 10 секунд, будет зациклено
    const sampleRate = ctx.sampleRate;
    const length = Math.floor(sampleRate * duration);
    const buffer = ctx.createBuffer(1, length, sampleRate);
    const data = buffer.getChannelData(0);

    for (let i = 0; i < length; i++) {
      const t = i / sampleRate;
      
      // Медленно эволюционирующие гармоники
      const f1 = Math.sin(2 * Math.PI * baseFreq * t + Math.sin(t * 0.1) * 0.5) * 0.3;
      const f2 = Math.sin(2 * Math.PI * baseFreq * 1.5 * t + Math.sin(t * 0.07) * 0.4) * 0.2;
      const f3 = Math.sin(2 * Math.PI * baseFreq * 2.01 * t + Math.sin(t * 0.13) * 0.3) * 0.1;
      
      // Модуляция амплитуды для "дыхания"
      const breath = 0.7 + Math.sin(t * 0.05) * 0.15 + Math.sin(t * 0.13) * 0.1;
      
      data[i] = (f1 + f2 + f3) * breath * 0.15;
    }

    return buffer;
  }

  /**
   * Звук получения награды/достижения
   */
  generateAchievementSound() {
    const ctx = this.ctx;
    const duration = 1.8;
    const sampleRate = ctx.sampleRate;
    const length = Math.floor(sampleRate * duration);
    const buffer = ctx.createBuffer(1, length, sampleRate);
    const data = buffer.getChannelData(0);

    // "Хрустальный" каскад нот
    const notes = [523, 659, 784, 1047, 1319, 1568]; // C5 → G6
    const noteDuration = duration / notes.length;

    for (let i = 0; i < length; i++) {
      const t = i / sampleRate;
      const noteIndex = Math.min(Math.floor(t / noteDuration), notes.length - 1);
      const noteT = t - noteIndex * noteDuration;
      const freq = notes[noteIndex];
      
      const envelope = Math.exp(-noteT * 5) * (1 - Math.exp(-noteT * 50));
      
      // Колокольный тембр (синус + обертоны)
      const bell = Math.sin(2 * Math.PI * freq * noteT) * 0.5 +
                   Math.sin(2 * Math.PI * freq * 2.76 * noteT) * 0.2 +
                   Math.sin(2 * Math.PI * freq * 5.4 * noteT) * 0.1;
      
      // Глобальная огибающая
      const globalEnv = Math.exp(-t * 1.2);
      
      data[i] = bell * envelope * globalEnv * 0.4;
    }

    return buffer;
  }
}

export const synth = new Synth();