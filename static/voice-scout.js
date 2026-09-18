(function () {
  var configEl = document.getElementById('scout-config');
  if (!configEl) return;

  var config = JSON.parse(configEl.textContent) || {};
  var teams = config.teams || [];
  var oneRobot = !!config.one_robot;
  var focusTeam = oneRobot && teams[0] ? teams[0].number : '';
  var categories = ['auto', 'scoring', 'driving', 'defense', 'reliability', 'other'];
  var categoryLabels = {
    auto: 'Auto',
    scoring: 'Scoring',
    driving: 'Driving',
    defense: 'Defense',
    reliability: 'Reliability',
    other: 'Other'
  };

  var voice = {
    used: false,
    ws: null,
    stream: null,
    audioCtx: null,
    processor: null,
    source: null,
    finals: [],
    interim: '',
    items: [],
    talkingAbout: '',
    ready: false
  };

  var toggleBtn = document.getElementById('voice-toggle');
  var statusEl = document.getElementById('voice-status');
  var talkingEl = document.getElementById('voice-talking');
  var talkingTeamEl = document.getElementById('voice-talking-team');
  var captionEl = document.getElementById('voice-caption');
  var nextBtn = document.getElementById('next-match-btn');
  var review = document.getElementById('review-overlay');
  var reviewTranscript = document.getElementById('review-transcript');
  var reviewTeams = document.getElementById('review-teams');
  var reviewError = document.getElementById('review-error');

  function textareaFor(team) {
    var card = document.querySelector('[data-team-card="' + team + '"]');
    return card ? card.querySelector('textarea') : null;
  }

  function catsFor(team) {
    var card = document.querySelector('[data-team-card="' + team + '"]');
    return card ? card.querySelector('[data-team-cats]') : null;
  }

  function setStatus(text, listening) {
    if (statusEl) statusEl.textContent = text;
    var bar = document.getElementById('voice-bar');
    if (bar) bar.classList.toggle('is-listening', !!listening);
    if (toggleBtn) {
      toggleBtn.textContent = listening ? 'Stop listening' : 'Start listening';
      toggleBtn.setAttribute('aria-pressed', listening ? 'true' : 'false');
    }
  }

  function fullTranscript() {
    var parts = voice.finals.slice();
    if (voice.interim) parts.push(voice.interim);
    return parts.join(' ').replace(/\s+/g, ' ').trim();
  }

  function emptyCaption() {
    return oneRobot ? 'Talk, or type in the notes box.' : 'Tap a notes box, then talk. Saying “team 1234” also switches.';
  }

  function renderCaption() {
    if (captionEl) captionEl.textContent = fullTranscript() || emptyCaption();
  }

  function highlightTeam(team) {
    if (oneRobot) {
      voice.talkingAbout = focusTeam;
      return;
    }
    voice.talkingAbout = team || '';
    document.querySelectorAll('[data-team-card]').forEach(function (card) {
      var on = team && card.getAttribute('data-team-card') === team;
      card.classList.toggle('is-talking-about', on);
    });
    if (!talkingEl || !talkingTeamEl) return;
    if (!team) {
      talkingEl.classList.add('invisible');
      talkingTeamEl.textContent = '—';
      return;
    }
    var meta = teams.find(function (t) { return t.number === team; });
    talkingTeamEl.textContent = 'Team ' + team;
    talkingTeamEl.classList.toggle('text-red-700', meta && meta.alliance === 'Red');
    talkingTeamEl.classList.toggle('text-blue-700', meta && meta.alliance === 'Blue');
    talkingEl.classList.remove('invisible');
  }

  function sendFocus(team) {
    if (!team) return;
    if (voice.ws && voice.ws.readyState === 1) {
      try { voice.ws.send(JSON.stringify({ type: 'focus', team: team })); } catch (e) {}
    }
  }

  function selectTeam(team) {
    if (oneRobot || !team) return;
    highlightTeam(team);
    sendFocus(team);
  }

  function activateTag(team, tag) {
    var card = document.querySelector('[data-team-card="' + team + '"]');
    if (!card) return;
    var btns = card.querySelectorAll('[data-tag-btn]');
    for (var i = 0; i < btns.length; i++) {
      if (btns[i].getAttribute('data-tag') === tag) {
        btns[i].classList.add('active');
        return;
      }
    }
  }

  function tagsFromText(text) {
    var t = String(text || '');
    var tags = [];
    if (/\b(broke|broken|broke down|disabled)\b/i.test(t)) tags.push('Broke');
    if (/\b(was defended|being defended|got defended|against (?:some )?defense|struggling .{0,40}defense|avoid .{0,80}defense)\b/i.test(t)) {
      tags.push('Was Defended');
    }
    if (/\b(played defense|playing defense|plays defense)\b/i.test(t)) tags.push('Played Defense');
    return tags;
  }

  function applyQuickTags(team, text) {
    if (!team) return;
    tagsFromText(text).forEach(function (tag) { activateTag(team, tag); });
  }

  function applyQuickTagsFromNotes() {
    teams.forEach(function (t) {
      var bits = [];
      (voice.items || []).forEach(function (n) {
        if (n.team === t.number && n.text) bits.push(n.text);
      });
      var ta = textareaFor(t.number);
      if (ta && ta.value) bits.push(ta.value);
      applyQuickTags(t.number, bits.join(' '));
    });
  }

  function renderCats(team, items) {
    var host = catsFor(team);
    if (!host) return;
    var byCat = {};
    (items || []).forEach(function (n) {
      if (n.team !== team || !n.text) return;
      (byCat[n.category] || (byCat[n.category] = [])).push(n.text);
    });
    host.innerHTML = '';
    categories.forEach(function (cat) {
      var lines = byCat[cat];
      if (!lines || !lines.length) return;
      var row = document.createElement('div');
      row.className = 'rounded-lg bg-white/80 px-2 py-1 border border-[#E0CDA8]';
      row.innerHTML = '<span class="font-black uppercase tracking-wide text-[#8D6E63]">' +
        categoryLabels[cat] + ':</span> <span class="text-[#5D4037]">' +
        escapeHtml(lines.join('; ')) + '</span>';
      host.appendChild(row);
    });
  }

  function applyNotes(payload) {
    if (!payload) return;
    if (payload.current_team) highlightTeam(payload.current_team);
    voice.items = payload.items || [];
    var notes = payload.notes || {};
    teams.forEach(function (t) {
      var ta = textareaFor(t.number);
      if (ta && notes[t.number] != null && !ta.dataset.userEdited) {
        ta.value = notes[t.number];
      }
      renderCats(t.number, voice.items);
    });
    applyQuickTagsFromNotes();
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c];
    });
  }

  function to16kPCM(float32, inRate) {
    var sampleRate = inRate || 16000;
    var input = float32;
    if (sampleRate !== 16000) {
      var ratio = sampleRate / 16000;
      var outLen = Math.max(1, Math.floor(float32.length / ratio));
      var resampled = new Float32Array(outLen);
      for (var i = 0; i < outLen; i++) resampled[i] = float32[Math.min(float32.length - 1, Math.floor(i * ratio))];
      input = resampled;
    }
    var pcm = new Int16Array(input.length);
    for (var j = 0; j < input.length; j++) {
      var s = Math.max(-1, Math.min(1, input[j]));
      pcm[j] = s < 0 ? s * 0x8000 : s * 0x7fff;
    }
    return pcm;
  }

  function wsURL() {
    var proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    var nums = teams.map(function (t) { return t.number; }).join(',');
    var als = teams.map(function (t) { return t.alliance; }).join(',');
    var q = 'teams=' + encodeURIComponent(nums) + '&alliances=' + encodeURIComponent(als);
    if (focusTeam) q += '&focus_team=' + encodeURIComponent(focusTeam);
    return proto + '//' + location.host + '/api/voice-scout?' + q;
  }

  async function startListening() {
    if (voice.ws) return;
    setStatus('Requesting microphone…', false);
    try {
      voice.stream = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, channelCount: 1 }
      });
    } catch (err) {
      setStatus('Mic blocked — type notes instead, or allow the microphone.', false);
      return;
    }

    voice.ws = new WebSocket(wsURL());
    voice.ws.binaryType = 'arraybuffer';
    voice.ready = false;

    voice.ws.onopen = function () {
      setStatus('Connecting to transcription…', true);
    };
    voice.ws.onerror = function () {
      setStatus('Voice connection failed — you can still type.', false);
    };
    voice.ws.onclose = function () {
      teardownAudio();
      voice.ws = null;
      voice.ready = false;
      if (!review || review.classList.contains('hidden')) {
        setStatus(voice.used ? 'Listening paused. Tap to continue.' : 'Mic off — tap to dictate notes.', false);
      }
    };
    voice.ws.onmessage = function (ev) {
      var msg;
      try { msg = JSON.parse(ev.data); } catch (e) { return; }
      if (msg.type === 'ready') {
        voice.ready = true;
        voice.used = true;
        setStatus(oneRobot ? 'Listening.' : 'Listening — tap a notes box, then talk.', true);
        if (nextBtn) nextBtn.textContent = 'Review & next →';
        sendFocus(oneRobot ? focusTeam : voice.talkingAbout);
      } else if (msg.type === 'interim') {
        voice.interim = msg.text || '';
        renderCaption();
      } else if (msg.type === 'final') {
        if (msg.text) voice.finals.push(msg.text);
        voice.interim = '';
        renderCaption();
        applyQuickTags(oneRobot ? focusTeam : voice.talkingAbout, msg.text);
      } else if (msg.type === 'talking_about') {
        highlightTeam(msg.team);
      } else if (msg.type === 'notes') {
        applyNotes(msg);
      } else if (msg.type === 'error') {
        setStatus(msg.text || 'Voice error', false);
      }
    };

    var AudioCtx = window.AudioContext || window.webkitAudioContext;
    voice.audioCtx = new AudioCtx();
    if (voice.audioCtx.state === 'suspended') await voice.audioCtx.resume();
    voice.source = voice.audioCtx.createMediaStreamSource(voice.stream);
    voice.processor = voice.audioCtx.createScriptProcessor(4096, 1, 1);
    voice.processor.onaudioprocess = function (e) {
      if (!voice.ws || voice.ws.readyState !== 1 || !voice.ready) return;
      var pcm = to16kPCM(e.inputBuffer.getChannelData(0), voice.audioCtx.sampleRate);
      voice.ws.send(pcm.buffer);
    };
    var mute = voice.audioCtx.createGain();
    mute.gain.value = 0;
    voice.source.connect(voice.processor);
    voice.processor.connect(mute);
    mute.connect(voice.audioCtx.destination);
    setStatus('Starting microphone…', true);
  }

  function teardownAudio() {
    try { if (voice.processor) voice.processor.disconnect(); } catch (e) {}
    try { if (voice.source) voice.source.disconnect(); } catch (e) {}
    try { if (voice.audioCtx) voice.audioCtx.close(); } catch (e) {}
    if (voice.stream) voice.stream.getTracks().forEach(function (t) { t.stop(); });
    voice.processor = null;
    voice.source = null;
    voice.audioCtx = null;
    voice.stream = null;
  }

  function stopListening() {
    if (voice.ws && voice.ws.readyState === 1) {
      try { voice.ws.send(JSON.stringify({ type: 'stop' })); } catch (e) {}
      voice.ws.close();
    }
    voice.ws = null;
    teardownAudio();
    setStatus(voice.used ? 'Review your notes, or listen again.' : 'Mic off — tap to dictate notes.', false);
  }

  function itemsFromReview() {
    var items = [];
    teams.forEach(function (t) {
      categories.forEach(function (cat) {
        var el = document.getElementById('review-' + t.number + '-' + cat);
        if (!el) return;
        var text = (el.value || '').trim();
        if (text) items.push({ team: t.number, category: cat, text: text });
      });
    });
    return items;
  }

  function notesMapFromItems(items) {
    var grouped = {};
    (items || []).forEach(function (n) {
      (grouped[n.team] || (grouped[n.team] = [])).push(n);
    });
    var out = {};
    Object.keys(grouped).forEach(function (team) {
      var byCat = {};
      grouped[team].forEach(function (n) {
        (byCat[n.category] || (byCat[n.category] = [])).push(n.text);
      });
      var parts = [];
      categories.forEach(function (cat) {
        if (!byCat[cat]) return;
        parts.push(categoryLabels[cat] + ': ' + byCat[cat].join('; '));
      });
      out[team] = parts.join('\n');
    });
    return out;
  }

  function fillReview(sorted) {
    reviewTeams.innerHTML = '';
    var items = (sorted && sorted.items) || voice.items || [];
    var byTeamCat = {};
    items.forEach(function (n) {
      if (!n.team || !n.text) return;
      byTeamCat[n.team] = byTeamCat[n.team] || {};
      byTeamCat[n.team][n.category] = (byTeamCat[n.team][n.category] || []).concat([n.text]);
    });
    var grid = document.createElement('div');
    grid.className = oneRobot ? 'space-y-4' : 'grid grid-cols-1 md:grid-cols-2 gap-3';
    teams.forEach(function (t) {
      var existing = (byTeamCat[t.number] || {});
      var ta = textareaFor(t.number);
      if (ta && ta.dataset.userEdited && ta.value.trim()) {
        existing = { other: [ta.value.trim()] };
      } else {
        var hasAny = Object.keys(existing).some(function (k) { return existing[k] && existing[k].length; });
        if (!hasAny && ta && ta.value.trim()) {
          existing = { other: [ta.value.trim()] };
        }
      }
      byTeamCat[t.number] = existing;
      var card = document.createElement('div');
      card.className = 'rounded-2xl border-2 p-3 bg-[#FFFBF5] ' +
        (t.alliance === 'Red' ? 'border-red-300' : 'border-blue-300');
      var title = document.createElement('h3');
      title.className = 'font-black uppercase mb-2 ' + (t.alliance === 'Red' ? 'text-red-700' : 'text-blue-700');
      title.textContent = 'Team ' + t.number;
      card.appendChild(title);
      categories.forEach(function (cat) {
        var wrap = document.createElement('label');
        wrap.className = 'block mb-2';
        wrap.innerHTML = '<span class="block text-[10px] font-black uppercase tracking-wide text-[#A1887F] mb-1">' +
          categoryLabels[cat] + '</span>';
        var ta = document.createElement('textarea');
        ta.id = 'review-' + t.number + '-' + cat;
        ta.rows = oneRobot ? 3 : 2;
        ta.className = 'w-full p-2 text-sm bg-white border border-[#E0CDA8] rounded-lg resize-y focus:outline-none focus:border-[#8D6E63]';
        ta.value = ((byTeamCat[t.number] || {})[cat] || []).join('\n');
        wrap.appendChild(ta);
        card.appendChild(wrap);
      });
      grid.appendChild(card);
    });
    reviewTeams.appendChild(grid);
    reviewTranscript.textContent = voice.finals.join(' ').replace(/\s+/g, ' ').trim() || '(no speech captured)';
  }

  async function openReview() {
    stopListening();
    reviewError.textContent = '';
    review.classList.remove('hidden');
    document.body.classList.add('overflow-hidden');
    fillReview({ items: voice.items });
  }

  function closeReview() {
    review.classList.add('hidden');
    document.body.classList.remove('overflow-hidden');
  }

  function confirmReview() {
    var notes = notesMapFromItems(itemsFromReview());
    teams.forEach(function (t) {
      var ta = textareaFor(t.number);
      if (ta) {
        ta.dataset.userEdited = '';
        ta.value = notes[t.number] || ta.value;
      }
    });
    applyQuickTagsFromNotes();
    closeReview();
    window.saveAndAdvance();
  }

  document.querySelectorAll('[data-team-card] textarea').forEach(function (ta) {
    ta.addEventListener('input', function () { ta.dataset.userEdited = '1'; });
  });

  if (!oneRobot) {
    document.querySelectorAll('[data-team-card]').forEach(function (card) {
      card.addEventListener('pointerdown', function () {
        selectTeam(card.getAttribute('data-team-card'));
      });
    });
  }

  if (toggleBtn) {
    toggleBtn.addEventListener('click', function () {
      if (voice.ws) stopListening();
      else startListening();
    });
  }

  var confirmBtn = document.getElementById('review-confirm');
  var backBtn = document.getElementById('review-back');
  if (confirmBtn) confirmBtn.addEventListener('click', confirmReview);
  if (backBtn) backBtn.addEventListener('click', closeReview);

  window.voiceScout = {
    used: function () { return voice.used || !!fullTranscript(); },
    openReview: openReview,
    stopListening: stopListening
  };

  renderCaption();
  highlightTeam(focusTeam);
})();
