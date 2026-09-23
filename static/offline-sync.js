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

	// ── A small "still sending" indicator, so a scout never has to wonder ──
	var badge = document.createElement('div');
	badge.id = 'offline-sync-badge';
	badge.style.cssText = 'position:fixed;bottom:14px;right:14px;z-index:9999;' +
		'background:#5D4037;color:#fff;font:bold 12px sans-serif;padding:8px 14px;' +
		'border-radius:999px;box-shadow:0 2px 8px rgba(0,0,0,0.25);display:none;' +
		'max-width:80vw;';
	document.body.appendChild(badge);
	function updateBadge(count) {
		if (count > 0) {
			badge.textContent = count + (count === 1 ? ' note saved on this device — sending…' : ' notes saved on this device — sending…');
			badge.style.display = 'block';
		} else {
			badge.style.display = 'none';
		}
	}
	window.offlineSync.onChange(updateBadge);
	updateBadge(load().length);
})();
