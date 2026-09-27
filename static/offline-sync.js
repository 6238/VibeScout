// offline-sync.js — save-to-device-first, sync-when-possible.
//
// A scouting form calls window.offlineSync.queue(...) instead of fetch()
// directly. The submission is written to localStorage immediately (which
// can't fail or depend on the network), so nothing typed is ever lost to bad
// wifi. It's then sent in the background: right away if the connection is
// good, and retried automatically — when the browser notices it's back
// online, and on a periodic timer regardless, in case that event doesn't
// fire — until the server confirms it. Loaded on every page (see
// layout.templ), so the queue keeps draining no matter where the scout
// navigates to next.
(function () {
	var STORAGE_KEY = 'vibescout_pending_v1';
	var TIMEOUT_MS = 12000;
	var RETRY_INTERVAL_MS = 15000;

	function load() {
		try {
			var raw = localStorage.getItem(STORAGE_KEY);
			return raw ? JSON.parse(raw) : [];
		} catch (e) {
			return []; // private browsing, storage disabled, or corrupt JSON
		}
	}
	function save(items) {
		try {
			localStorage.setItem(STORAGE_KEY, JSON.stringify(items));
		} catch (e) {
			// Storage full or unavailable: the in-memory attempt this page load
			// still goes out via flush(); we just can't guarantee it survives a
			// reload. Nothing else to do about it here.
		}
	}
	function genId() {
		if (window.crypto && crypto.randomUUID) return crypto.randomUUID();
		return 'id-' + Date.now() + '-' + Math.random().toString(16).slice(2);
	}

	var onSyncedCbs = [];
	var onChangeCbs = [];
	var inFlight = {}; // item id -> true while its request is outstanding

	function notifyChange() {
		var count = load().length;
		onChangeCbs.forEach(function (cb) {
			try { cb(count); } catch (e) { /* one bad listener shouldn't break the rest */ }
		});
	}

	// queue saves a submission locally and returns its id. `encoding` is
	// 'json' (default — sent as a JSON body) or 'form' (sent as
	// application/x-www-form-urlencoded, for endpoints that read r.FormValue).
	// For kind 'scout', a submission_id field is added to the payload so the
	// server can dedupe retries of the same attempt.
	function queue(kind, url, payload, encoding) {
		var id = genId();
		if (kind === 'scout') {
			payload = Object.assign({}, payload, { submission_id: id });
		}
		var items = load();
		items.push({ id: id, kind: kind, url: url, payload: payload, encoding: encoding || 'json', createdAt: Date.now() });
		save(items);
		notifyChange();
		flush();
		return id;
	}

	function removeItem(id) {
		save(load().filter(function (it) { return it.id !== id; }));
		notifyChange();
	}

	function sendOne(item) {
		if (inFlight[item.id]) return;
		inFlight[item.id] = true;

		var controller = window.AbortController ? new AbortController() : null;
		var timer = controller ? setTimeout(function () { controller.abort(); }, TIMEOUT_MS) : null;
		var opts = { method: 'POST', signal: controller ? controller.signal : undefined };
		if (item.encoding === 'form') {
			var body = new URLSearchParams();
			Object.keys(item.payload).forEach(function (k) { body.append(k, item.payload[k]); });
			opts.body = body; // fetch sets the correct content-type for URLSearchParams itself
		} else {
			opts.headers = { 'Content-Type': 'application/json' };
			opts.body = JSON.stringify(item.payload);
		}

		fetch(item.url, opts).then(function (resp) {
			if (timer) clearTimeout(timer);
			delete inFlight[item.id];
			if (resp.ok) {
				removeItem(item.id);
				onSyncedCbs.forEach(function (cb) {
					try { cb(item); } catch (e) { /* ditto */ }
				});
			}
			// Not ok: leave it queued. A real, repeatable server error would keep
			// failing on every retry too, which is at least visible (the pending
			// badge never clears) rather than the data silently vanishing.
		}).catch(function () {
			if (timer) clearTimeout(timer);
			delete inFlight[item.id];
			// Network error or our own timeout: leave it queued, retried later.
		});
	}

	function flush() {
		load().forEach(sendOne);
	}

	window.addEventListener('online', flush);
	setInterval(flush, RETRY_INTERVAL_MS);
	flush(); // pick up anything left over from a previous page load

	window.offlineSync = {
		queue: queue,
		pendingCount: function () { return load().length; },
		onSynced: function (cb) { onSyncedCbs.push(cb); },
		onChange: function (cb) { onChangeCbs.push(cb); }
	};

	// ── Sync/connection status: a scout on bad wifi should never have to
	//    wonder whether their data actually made it. Three states — offline
	//    (saved locally, nothing to send it over yet), syncing (queued,
	//    actively sending), and a brief "synced" confirmation the instant the
	//    queue actually clears — then it disappears, since a confirmation
	//    you've already seen has nothing left to say. ──
	var badge = document.createElement('div');
	badge.id = 'offline-sync-badge';
	badge.setAttribute('role', 'status');
	badge.setAttribute('aria-live', 'polite');
	document.body.appendChild(badge);

	var style = document.createElement('style');
	style.textContent =
		'#offline-sync-badge{position:fixed;bottom:14px;right:14px;z-index:9999;' +
		'display:inline-flex;align-items:center;gap:7px;font:600 12px "IBM Plex Sans",ui-sans-serif,sans-serif;' +
		'padding:9px 14px;border-radius:999px;box-shadow:0 4px 14px -4px rgba(12,8,54,.35);' +
		'max-width:80vw;opacity:0;transform:translateY(8px) scale(.96);' +
		'transition:opacity .2s ease,transform .2s ease,background-color .2s ease;pointer-events:none;}' +
		'#offline-sync-badge.is-visible{opacity:1;transform:translateY(0) scale(1);}' +
		'#offline-sync-badge.is-offline{background:var(--ps-gold-strong,#c99a2e);color:#fff;}' +
		'#offline-sync-badge.is-syncing{background:var(--ps-ink,#0c0836);color:#fff;}' +
		'#offline-sync-badge.is-synced{background:#2f7a4f;color:#fff;}' +
		'#offline-sync-badge svg{flex:none;}' +
		'.ps-sync-spin{animation:ps-sync-spin 1s linear infinite;}' +
		'@keyframes ps-sync-spin{to{transform:rotate(360deg);}}';
	document.head.appendChild(style);

	var ICONS = {
		offline: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M2 2l20 20"/><path d="M8.5 16.5a5 5 0 0 1 7 0"/><path d="M5 12.5a10 10 0 0 1 3-2.1"/><path d="M12 20h.01"/><path d="M16 8.5a10 10 0 0 1 3 2"/><path d="M19.5 12a13 13 0 0 0-2-1.8"/></svg>',
		syncing: '<svg class="ps-sync-spin" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-3-6.7"/><path d="M21 3v6h-6"/></svg>',
		synced: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.25" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6L9 17l-5-5"/></svg>'
	};

	var hideTimer = null;
	function showBadge(state, text) {
		clearTimeout(hideTimer);
		badge.className = 'is-visible is-' + state;
		badge.innerHTML = ICONS[state] + '<span>' + text + '</span>';
	}
	function hideBadge() {
		badge.classList.remove('is-visible');
	}

	var wasPending = false;
	function updateBadge(count) {
		if (!navigator.onLine) {
			showBadge('offline', count > 0
				? count + (count === 1 ? ' note' : ' notes') + ' saved — offline'
				: 'Offline — saving locally');
			wasPending = count > 0;
			return;
		}
		if (count > 0) {
			showBadge('syncing', 'Syncing ' + count + (count === 1 ? ' note…' : ' notes…'));
			wasPending = true;
		} else if (wasPending) {
			// Just cleared: a real confirmation, not silence, then fade out.
			showBadge('synced', 'All synced');
			wasPending = false;
			hideTimer = setTimeout(hideBadge, 2200);
		} else {
			hideBadge();
		}
	}
	window.offlineSync.onChange(updateBadge);
	window.addEventListener('online', function () { updateBadge(window.offlineSync.pendingCount()); });
	window.addEventListener('offline', function () { updateBadge(window.offlineSync.pendingCount()); });
	updateBadge(load().length);
})();
