// The fun part, and only that. Everything here decorates an app that works the
// same without it: no handler prevents a default, stops an event or delays a
// request, so nothing in this file can ever cost anyone a click.
(function () {
  "use strict";

  var TAU = Math.PI * 2;
  var SPRING = "cubic-bezier(.34, 1.56, .64, 1)";
  var GLIDE = "cubic-bezier(.22, 1, .36, 1)";
  var motion = window.matchMedia("(prefers-reduced-motion: reduce)");

  function calm() { return motion.matches; }

  function all(selector, root) {
    return Array.prototype.slice.call((root || document).querySelectorAll(selector));
  }

  function rand(low, high) { return low + Math.random() * (high - low); }

  function pick(list) { return list[Math.floor(Math.random() * list.length)]; }

  function cssVar(name, el) {
    return getComputedStyle(el || document.documentElement).getPropertyValue(name).trim();
  }

  // Particles ---------------------------------------------------------------
  //
  // One canvas over the whole page, drawing only while something is in flight.
  // It hangs off <body>, so the region swaps in app.js never touch it.

  var canvas = null;
  var pen = null;
  var flying = [];
  var lastFrame = 0;
  var ratio = 1;

  function launch(particle) {
    if (calm() || flying.length > 500) return;
    if (!canvas) {
      canvas = document.createElement("canvas");
      canvas.className = "fx-canvas";
      canvas.setAttribute("aria-hidden", "true");
      document.body.appendChild(canvas);
      pen = canvas.getContext("2d");
    }
    particle.age = 0;
    flying.push(particle);
    if (flying.length === 1) {
      // Sized at the start of each flight, so there is no resize listener.
      ratio = window.devicePixelRatio || 1;
      canvas.width = window.innerWidth * ratio;
      canvas.height = window.innerHeight * ratio;
      lastFrame = performance.now();
      requestAnimationFrame(frame);
    }
  }

  function frame(now) {
    // Capped, so a tab returning from the background does not skip the flight.
    var step = Math.min((now - lastFrame) / 1000, 0.05);
    lastFrame = now;
    pen.setTransform(ratio, 0, 0, ratio, 0, 0);
    pen.clearRect(0, 0, window.innerWidth, window.innerHeight);
    flying = flying.filter(function (p) {
      p.age += step;
      if (p.age >= p.life) return false;
      draw[p.kind](p, p.age / p.life, step);
      return true;
    });
    if (flying.length) requestAnimationFrame(frame);
    else pen.clearRect(0, 0, window.innerWidth, window.innerHeight);
  }

  function easeOut(t) { return 1 - Math.pow(1 - t, 3); }

  var shapes = {
    star: function (size) {
      pen.beginPath();
      for (var i = 0; i < 10; i++) {
        var reach = i % 2 ? size * 0.45 : size;
        var angle = (i / 10) * TAU - Math.PI / 2;
        pen.lineTo(Math.cos(angle) * reach, Math.sin(angle) * reach);
      }
      pen.closePath();
      pen.fill();
    },
    sparkle: function (size) {
      pen.beginPath();
      for (var i = 0; i < 8; i++) {
        var reach = i % 2 ? size * 0.22 : size;
        var angle = (i / 8) * TAU;
        pen.lineTo(Math.cos(angle) * reach, Math.sin(angle) * reach);
      }
      pen.closePath();
      pen.fill();
    },
    confetti: function (size) {
      pen.fillRect(-size / 2, -size / 4, size, size / 2);
    },
    dot: function (size) {
      pen.beginPath();
      pen.arc(0, 0, size / 2, 0, TAU);
      pen.fill();
    }
  };

  var draw = {
    // A comic-book impact line: the head shoots out, the tail catches it up.
    line: function (p, t) {
      var head = p.from + (p.to - p.from) * easeOut(t);
      var tail = p.from + (p.to - p.from) * t * t;
      pen.globalAlpha = 1;
      pen.strokeStyle = p.color;
      pen.lineWidth = p.width * (1 - t * 0.6);
      pen.lineCap = "round";
      pen.beginPath();
      pen.moveTo(p.x + Math.cos(p.angle) * tail, p.y + Math.sin(p.angle) * tail);
      pen.lineTo(p.x + Math.cos(p.angle) * head, p.y + Math.sin(p.angle) * head);
      pen.stroke();
    },
    ring: function (p, t) {
      pen.globalAlpha = 1 - t;
      pen.strokeStyle = p.color;
      pen.lineWidth = p.width * (1 - t);
      pen.beginPath();
      pen.arc(p.x, p.y, p.to * easeOut(t), 0, TAU);
      pen.stroke();
    },
    // Anything with weight: it flies, slows, falls and spins.
    bit: function (p, t, step) {
      p.vx *= 1 - 1.8 * step;
      p.vy = p.vy * (1 - 1.8 * step) + p.gravity * step;
      p.x += p.vx * step;
      p.y += p.vy * step;
      p.spin += p.turn * step;
      pen.save();
      pen.translate(p.x, p.y);
      pen.rotate(p.spin);
      pen.globalAlpha = t > 0.7 ? (1 - t) / 0.3 : 1;
      pen.fillStyle = p.color;
      shapes[p.shape](p.size * Math.min(1, t * 8));
      pen.restore();
    },
    puff: function (p, t) {
      pen.globalAlpha = (1 - t) * 0.55;
      pen.fillStyle = p.color;
      pen.beginPath();
      pen.arc(p.x + p.vx * easeOut(t), p.y + p.vy * easeOut(t), p.size * (0.4 + easeOut(t)), 0, TAU);
      pen.fill();
    },
    word: function (p, t) {
      var grow = Math.min(1, t * 6);
      pen.save();
      pen.translate(p.x, p.y - 46 * easeOut(t));
      pen.rotate(p.tilt);
      pen.scale(grow, grow);
      pen.globalAlpha = t > 0.7 ? (1 - t) / 0.3 : 1;
      pen.font = "700 19px " + cssVar("--font-fun");
      pen.textAlign = "center";
      pen.lineJoin = "round";
      pen.lineWidth = 5;
      pen.strokeStyle = cssVar("--card");
      pen.strokeText(p.text, 0, 0);
      pen.fillStyle = p.color;
      pen.fillText(p.text, 0, 0);
      pen.restore();
    }
  };

  // impact is the burst of lines that answers every click.
  function impact(x, y, colors, size) {
    var count = 10;
    var turn = rand(0, TAU);
    for (var i = 0; i < count; i++) {
      var long = i % 2 === 0;
      launch({
        kind: "line",
        x: x,
        y: y,
        angle: turn + (i / count) * TAU + rand(-0.12, 0.12),
        from: 7 * size,
        to: (long ? 38 : 24) * size * rand(0.85, 1.15),
        width: (long ? 3.5 : 2.5) * size,
        color: colors[i % colors.length],
        life: rand(0.3, 0.4)
      });
    }
    launch({ kind: "ring", x: x, y: y, to: 22 * size, width: 3 * size, color: colors[0], life: 0.3 });
  }

  function scatter(x, y, shape, colors, count, force, size) {
    for (var i = 0; i < count; i++) {
      var angle = rand(0, TAU);
      var speed = rand(0.45, 1) * force;
      launch({
        kind: "bit",
        shape: shape,
        x: x,
        y: y,
        vx: Math.cos(angle) * speed,
        // Biased upwards, so it arcs and falls rather than just spreading.
        vy: Math.sin(angle) * speed - force * 0.45,
        gravity: 620,
        spin: rand(0, TAU),
        turn: rand(-9, 9),
        size: size * rand(0.7, 1.2),
        color: pick(colors),
        life: rand(0.6, 1)
      });
    }
  }

  function dust(x, y, color) {
    for (var i = 0; i < 9; i++) {
      launch({
        kind: "puff",
        x: x,
        y: y,
        vx: rand(-44, 44),
        vy: rand(-36, 22),
        size: rand(7, 15),
        color: color,
        life: rand(0.35, 0.6)
      });
    }
  }

  function shout(x, y, text, color) {
    launch({ kind: "word", x: x, y: y - 16, text: text, color: color, tilt: rand(-0.12, 0.12), life: 0.95 });
  }

  function categoryColors() {
    return ["article", "video", "book", "film", "documentary", "podcast", "website"].map(function (slug) {
      return cssVar("--k-" + slug);
    });
  }

  // rain is the win screen, for the moment the whole queue is finished.
  function rain() {
    var colors = categoryColors().concat(cssVar("--star"));
    for (var i = 0; i < 110; i++) {
      launch({
        kind: "bit",
        shape: "confetti",
        x: rand(0, window.innerWidth),
        y: rand(-120, -10),
        vx: rand(-50, 50),
        vy: rand(90, 300),
        gravity: 340,
        spin: rand(0, TAU),
        turn: rand(-8, 8),
        size: rand(7, 12),
        color: pick(colors),
        life: rand(1.4, 2.4)
      });
    }
  }

  // Sound -------------------------------------------------------------------
  //
  // Synthesised rather than recorded, so there are no audio files to embed. The
  // context is only created on a click, which is when browsers allow one.

  var SOUND_KEY = "catalogue-sound";
  var audio = null;
  var master = null;

  // localStorage throws in some private windows; sound is then simply on.
  function soundOn() {
    try { return localStorage.getItem(SOUND_KEY) !== "off"; } catch (e) { return true; }
  }

  function setSound(on) {
    try { localStorage.setItem(SOUND_KEY, on ? "on" : "off"); } catch (e) { /* see soundOn */ }
  }

  function speaker() {
    if (!soundOn()) return null;
    if (!audio) {
      var Context = window.AudioContext || window.webkitAudioContext;
      if (!Context) return null;
      audio = new Context();
      master = audio.createGain();
      master.gain.value = 0.5;
      master.connect(audio.destination);
    }
    if (audio.state === "suspended") audio.resume();
    return audio;
  }

  // note plays one blip, sliding between two pitches and dying away.
  function note(from, to, length, volume, wave, delay) {
    var ctx = speaker();
    if (!ctx) return;
    var at = ctx.currentTime + (delay || 0);
    var osc = ctx.createOscillator();
    var amp = ctx.createGain();
    osc.type = wave || "sine";
    osc.frequency.setValueAtTime(from, at);
    osc.frequency.exponentialRampToValueAtTime(to, at + length);
    amp.gain.setValueAtTime(0.0001, at);
    amp.gain.exponentialRampToValueAtTime(volume, at + 0.004);
    amp.gain.exponentialRampToValueAtTime(0.0001, at + length);
    osc.connect(amp);
    amp.connect(master);
    osc.start(at);
    osc.stop(at + length + 0.02);
  }

  // A soft breath of filtered noise, for the panel sliding.
  function swish() {
    var ctx = speaker();
    if (!ctx) return;
    var length = 0.16;
    var samples = Math.floor(ctx.sampleRate * length);
    var buffer = ctx.createBuffer(1, samples, ctx.sampleRate);
    var data = buffer.getChannelData(0);
    for (var i = 0; i < samples; i++) data[i] = Math.random() * 2 - 1;
    var source = ctx.createBufferSource();
    var filter = ctx.createBiquadFilter();
    var amp = ctx.createGain();
    var at = ctx.currentTime;
    source.buffer = buffer;
    filter.type = "bandpass";
    filter.Q.value = 1.2;
    filter.frequency.setValueAtTime(700, at);
    filter.frequency.exponentialRampToValueAtTime(2600, at + length);
    amp.gain.setValueAtTime(0.0001, at);
    amp.gain.exponentialRampToValueAtTime(0.28, at + 0.05);
    amp.gain.exponentialRampToValueAtTime(0.0001, at + length);
    source.connect(filter);
    filter.connect(amp);
    amp.connect(master);
    source.start(at);
  }

  // Clicks in quick succession climb in pitch and size, a semitone at a time.
  var combo = 0;
  var lastClick = 0;

  function climb() {
    var now = performance.now();
    combo = now - lastClick < 450 ? Math.min(combo + 1, 12) : 0;
    lastClick = now;
  }

  function pitch() { return Math.pow(2, combo / 12); }

  // Plick on the way down, plock on the way up.
  var sounds = {
    plick: function () { note(2300 * pitch(), 1300 * pitch(), 0.045, 0.3, "triangle"); },
    plock: function () {
      note(780 * pitch(), 330 * pitch(), 0.09, 0.55);
      note(1500 * pitch(), 700 * pitch(), 0.02, 0.14, "triangle");
    },
    tick: function () { note(1100, 800, 0.03, 0.09, "triangle"); },
    twinkle: function () {
      note(1319, 1319, 0.11, 0.26, "triangle");
      note(1976, 1976, 0.22, 0.26, "triangle", 0.075);
    },
    tuck: function () {
      [523, 659, 784].forEach(function (hz, i) {
        note(hz, hz, 0.14, 0.3, "triangle", i * 0.065);
      });
    },
    fanfare: function () {
      [523, 659, 784, 1047, 1319].forEach(function (hz, i) {
        note(hz, hz, i === 4 ? 0.42 : 0.16, 0.3, "triangle", i * 0.075);
      });
    },
    bloop: function () { note(460, 95, 0.24, 0.5); },
    swish: swish
  };

  // Clicks ------------------------------------------------------------------

  var CLICKABLE = "a, button, summary";
  var fromMouse = false;
  var acted = null;

  // Colours come from the page, so they follow dark mode and the category.
  function colorsFor(el) {
    var card = el.closest(".item");
    var kind = card ? cssVar("--kind", card) : "";
    return [kind || cssVar("--accent"), cssVar("--star")];
  }

  function celebrate(x, y) {
    impact(x, y, [cssVar("--good"), cssVar("--star")], 1.3);
    scatter(x, y, "confetti", categoryColors(), 34, 460, 10);
    shout(x, y, pick(["Done!", "Ta-da!", "Nice one!", "Woohoo!"]), cssVar("--good"));
    sounds.fanfare();
  }

  function save(el, x, y) {
    var adding = el.closest('form[action="/items"]');
    impact(x, y, [cssVar("--accent"), cssVar("--star")], 1.15);
    scatter(x, y, "sparkle", [cssVar("--star"), cssVar("--accent")], 12, 300, 11);
    if (adding) shout(x, y, pick(["Stashed!", "Got it!", "Tucked away!"]), cssVar("--accent"));
    sounds.tuck();
  }

  // react picks the effect that suits what was just clicked. The ordinary case
  // is the last one; the others are the moments worth making a fuss about.
  function react(el, x, y) {
    var size = 1 + combo * 0.07;
    var form = el.closest("form");
    if (el.matches(".star-toggle")) {
      if (el.matches(".on")) {
        impact(x, y, [cssVar("--muted")], 0.8);
        sounds.plock();
      } else {
        impact(x, y, [cssVar("--star")], 1.1);
        scatter(x, y, "star", [cssVar("--star")], 12, 330, 10);
        sounds.twinkle();
      }
    } else if (el.matches(".destructive")) {
      dust(x, y, cssVar("--muted"));
      sounds.bloop();
    } else if (form && form.querySelector('input[name="status"][value="done"]')) {
      celebrate(x, y);
    } else if (el.matches(".primary")) {
      save(el, x, y);
    } else if (el.matches(".list-toggle")) {
      impact(x, y, colorsFor(el), 1);
      if (!el.matches(".on")) scatter(x, y, "sparkle", colorsFor(el), 8, 240, 9);
      sounds.plock();
    } else if (el.matches(".notes-link, .panel-close, .panel-scrim")) {
      impact(x, y, colorsFor(el), size);
      sounds.swish();
    } else {
      impact(x, y, colorsFor(el), size);
      sounds.plock();
    }
    if (combo >= 6) scatter(x, y, "sparkle", colorsFor(el), combo, 280, 8);
  }

  document.addEventListener("pointerdown", function (e) {
    fromMouse = e.pointerType === "mouse";
    if (fromMouse && e.button === 0 && e.target.closest(CLICKABLE + ", select")) sounds.plick();
  });

  document.addEventListener("click", function (e) {
    var el = e.target.closest(CLICKABLE);
    if (!el) {
      // Even the empty space answers, quietly, unless you were aiming at text.
      if (e.target.closest("label, input, textarea, select")) return;
      if (!window.getSelection().isCollapsed) return;
      scatter(e.clientX, e.clientY, "dot", [cssVar("--line-strong")], 5, 110, 5);
      sounds.tick();
      return;
    }
    climb();
    // Touch and keyboard have no separate press, so they get both halves here.
    if (!fromMouse) sounds.plick();
    fromMouse = false;

    var form = el.closest("form");
    acted = form ? form.getAttribute("action") : null;

    // A keyboard click has no position of its own, so it comes from the middle.
    var x = e.clientX;
    var y = e.clientY;
    if (e.detail === 0) {
      var box = el.getBoundingClientRect();
      x = box.left + box.width / 2;
      y = box.top + box.height / 2;
    }
    react(el, x, y);
  });

  // The status dropdown on each row gets the same reward as the panel's buttons.
  document.addEventListener("change", function (e) {
    if (!e.target.matches(".status-form select")) return;
    var box = e.target.getBoundingClientRect();
    var x = box.left + box.width / 2;
    var y = box.top + box.height / 2;
    acted = e.target.form.getAttribute("action");
    if (e.target.value === "done") {
      celebrate(x, y);
    } else {
      impact(x, y, colorsFor(e.target), 1);
      sounds.plock();
    }
  });

  // Cmd/Ctrl+Enter saves without clicking anything, and deserves the same.
  document.addEventListener("keydown", function (e) {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter" && e.target.id === "notes") {
      var box = e.target.getBoundingClientRect();
      save(e.target, box.left + box.width / 2, box.bottom - 20);
    }
  });

  // Hover -------------------------------------------------------------------

  // A card shivers when the pointer arrives, and the nicest buttons fizz. The
  // settled state stays upright, so the text in a card is never left blurred.
  document.addEventListener("pointerover", function (e) {
    if (e.pointerType !== "mouse" || calm()) return;
    var card = e.target.closest(".items .item");
    if (card && !card.contains(e.relatedTarget)) {
      var lean = pick([-0.7, 0.7]);
      card.animate(
        [{ rotate: "0deg" }, { rotate: lean + "deg" }, { rotate: -lean * 0.5 + "deg" }, { rotate: "0deg" }],
        { duration: 460, easing: "ease-out" }
      );
    }
    var fizzy = e.target.closest(".primary, .star-toggle:not(.on)");
    if (fizzy && !fizzy.contains(e.relatedTarget)) {
      scatter(e.clientX, e.clientY, "sparkle", [cssVar("--star")], 4, 130, 7);
    }
  });

  // The glow inside a card follows the pointer, at most once a frame.
  var lastMove = null;

  document.addEventListener("pointermove", function (e) {
    if (e.pointerType !== "mouse" || calm()) return;
    if (!lastMove) requestAnimationFrame(moveGlow);
    lastMove = e;
  });

  function moveGlow() {
    var card = lastMove.target.closest ? lastMove.target.closest(".items .item") : null;
    if (card) {
      var box = card.getBoundingClientRect();
      card.style.setProperty("--mx", lastMove.clientX - box.left + "px");
      card.style.setProperty("--my", lastMove.clientY - box.top + "px");
    }
    lastMove = null;
  }

  // Region swaps ------------------------------------------------------------
  //
  // app.js replaces #main and the panel on every click, so all the elements are
  // new each time even when nothing visible changed. Comparing before and after
  // is what lets the page animate what really happened and hold still otherwise.

  var before = null;

  document.addEventListener("catalogue:swapping", function () {
    var cards = {};
    all(".items .item").forEach(function (el) {
      cards[el.dataset.item] = { el: el, box: el.getBoundingClientRect() };
    });
    var panel = document.querySelector("#panel-host .panel");
    var body = panel && panel.querySelector(".panel-body");
    var meter = document.querySelector(".xp");
    before = {
      cards: cards,
      panel: panel,
      scrim: document.querySelector("#panel-host .panel-scrim"),
      scroll: body ? body.scrollTop : 0,
      done: meter ? Number(meter.dataset.done) : null,
      total: meter ? Number(meter.dataset.total) : null
    };
  });

  document.addEventListener("catalogue:swapped", function () {
    var main = document.getElementById("main");
    if (main) main.classList.add("stay");
    settleCards(before.cards);
    settlePanel(before);
    settleMeter(before);
    hopActed();
    before = null;
    acted = null;
  });

  function onScreen(box) {
    return box.bottom > 0 && box.top < window.innerHeight;
  }

  // A card that was already there holds still, or glides if it moved. A new one
  // deals in. One that is gone fades out from where it was.
  function settleCards(old) {
    var dealt = 0;
    var kept = [];
    all(".items .item").forEach(function (el) {
      var was = old[el.dataset.item];
      if (was) {
        el.classList.add("stay");
        kept.push({ el: el, box: was.box });
        delete old[el.dataset.item];
      } else {
        el.style.setProperty("--i", Math.min(dealt++, 9));
      }
    });
    if (calm()) return;
    kept.forEach(function (card) {
      var now = card.el.getBoundingClientRect();
      var moved = card.box.top - now.top;
      if (Math.abs(moved) > 1 && (onScreen(card.box) || onScreen(now))) {
        card.el.animate(
          [{ translate: "0 " + moved + "px" }, { translate: "0 0" }],
          { duration: 420, easing: GLIDE }
        );
      }
    });
    Object.keys(old).forEach(function (id) { fadeCard(old[id]); });
  }

  function fadeCard(card) {
    if (!onScreen(card.box)) return;
    var el = card.el;
    el.classList.add("ghost");
    el.setAttribute("inert", "");
    el.style.left = card.box.left + "px";
    el.style.top = card.box.top + "px";
    el.style.width = card.box.width + "px";
    el.style.height = card.box.height + "px";
    document.body.appendChild(el);
    el.animate(
      [{ opacity: 1, scale: 1 }, { opacity: 0, scale: 0.9, translate: "0 10px" }],
      { duration: 200, easing: "ease-in", fill: "forwards" }
    ).onfinish = function () { el.remove(); };
  }

  function settlePanel(was) {
    var panel = document.querySelector("#panel-host .panel");
    var scrim = document.querySelector("#panel-host .panel-scrim");
    if (panel && was.panel && panel.dataset.panelId === was.panel.dataset.panelId) {
      panel.classList.add("stay");
      if (scrim) scrim.classList.add("stay");
      panel.querySelector(".panel-body").scrollTop = was.scroll;
    } else if (!panel && was.panel && !calm()) {
      slideAway(was.panel, "70px");
      slideAway(was.scrim, "0");
    }
  }

  // The closed panel lingers for a moment outside #panel-host, where app.js does
  // not look for it. Its ids go, so nothing can find this copy by mistake.
  function slideAway(el, distance) {
    all("[id]", el).forEach(function (node) { node.removeAttribute("id"); });
    el.setAttribute("inert", "");
    el.classList.add("stay");
    document.body.appendChild(el);
    el.animate(
      [{ opacity: 1, translate: "0 0" }, { opacity: 0, translate: distance + " 0" }],
      { duration: 190, easing: "ease-in", fill: "forwards" }
    ).onfinish = function () { el.remove(); };
  }

  // The experience bar slides to its new value, and sparkles if you earned it.
  // Deleting a queued item also raises the percentage, but that is no victory.
  function settleMeter(was) {
    var meter = document.querySelector(".xp");
    if (!meter || !was.total || calm()) return;
    var done = Number(meter.dataset.done);
    var from = (was.done / was.total) * 100;
    var to = (done / Number(meter.dataset.total)) * 100;
    if (from === to) return;
    meter.querySelector(".xp-fill").animate(
      [{ width: from + "%" }, { width: to + "%" }],
      { duration: 650, easing: SPRING }
    );
    if (done > was.done) {
      // Not while a panel is open: the bar is behind it, the canvas in front.
      if (!document.querySelector("#panel-host .panel")) {
        var box = meter.getBoundingClientRect();
        scatter(box.left + (box.width * to) / 100, box.top + box.height / 2, "sparkle", [cssVar("--star")], 10, 220, 9);
      }
      if (to === 100) {
        rain();
        sounds.fanfare();
      }
    }
  }

  // Whatever was just pressed has been rebuilt; give the new one a little hop,
  // and nudge its card, so the change is easy to spot.
  function hopActed() {
    if (!acted || calm()) return;
    all("form").filter(function (form) {
      return form.getAttribute("action") === acted;
    }).forEach(function (form) {
      var control = form.querySelector("button, select");
      if (control) {
        control.animate(
          [
            { scale: 1, rotate: "0deg" },
            { scale: 1.2, rotate: "-5deg" },
            { scale: 0.96, rotate: "2deg" },
            { scale: 1, rotate: "0deg" }
          ],
          { duration: 420, easing: "ease-out" }
        );
      }
      var card = form.closest(".items .item");
      if (card) {
        card.animate(
          [{ scale: 1 }, { scale: 1.018 }, { scale: 1 }],
          { duration: 300, easing: "ease-out" }
        );
      }
    });
  }

  // Dressing up what the server sent -----------------------------------------

  // Letters that can hop one at a time, read out as the one word they still are.
  all(".brand, .login h1").forEach(function (el) {
    var word = el.textContent;
    el.setAttribute("aria-label", word);
    el.textContent = "";
    word.split("").forEach(function (letter, i) {
      var span = document.createElement("span");
      span.textContent = letter;
      span.setAttribute("aria-hidden", "true");
      span.style.setProperty("--n", i);
      el.appendChild(span);
    });
  });

  // The switch lives here, not in the templates: without this script there is
  // no sound to turn off. It sits in the header, which swaps never replace.
  var signedIn = document.querySelector(".signed-in");
  if (signedIn) {
    var toggle = document.createElement("button");
    toggle.type = "button";
    toggle.className = "sound-toggle";
    toggle.textContent = "♪";
    var show = function () {
      var on = soundOn();
      toggle.setAttribute("aria-pressed", on ? "true" : "false");
      toggle.setAttribute("aria-label", "Sound effects");
      toggle.title = on ? "Sound is on" : "Sound is off";
    };
    show();
    // Runs before the document's click handler, so switching on is audible.
    toggle.addEventListener("click", function () {
      setSound(!soundOn());
      show();
    });
    signedIn.insertBefore(toggle, signedIn.firstChild);
  }
})();
