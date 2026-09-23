// read-cache.js — show the last-known answer instead of a stuck spinner.
//
// TBA schedules, Statbotics EPA/forecasts, and rankings barely change minute
// to minute, so there's no reason bad wifi should mean showing nothing. This
// caches every htmx GET fragment (match pickers, pit team list, the next-match
// forecast, team analysis cards) in localStorage as it loads successfully. If
// a later load of the same thing genuinely fails — connection error, bad
// response — the last cached copy is shown instead, clearly marked as stale,
// and it's refreshed automatically once the connection looks better. It does
// not impose its own timeout (see below), so a request that's merely slow —
// including the AI calls behind some of these — is left to finish rather than
// being cut off and treated as a failure.
//
// This only helps once a page has already loaded; it doesn't make the page
// itself load with zero connectivity (that would need a service worker
// caching the app shell, which we've deliberately not built — see the
// offline-wifi discussion this paired with).
(function () {
	if (!window.htmx) return; // load order issue, or htmx failed to load entirely

	var CACHE_PREFIX = 'vibescout_read_cache:';
	var RETRY_INTERVAL_MS = 15000;
	var requestURLs = new WeakMap(); // htmx element -> the exact URL it just requested

	// No global htmx.config.timeout here on purpose: some of these same GET
	// endpoints (/api/analyze-team, /api/next-match) make a real Gemini call
	// server-side and can legitimately take longer than a short timeout would
	// allow, even on good wifi. A blanket timeout would start falling back to
	// stale data for requests that were simply slow, not broken. We rely on
	// genuine failures instead — connection errors and non-2xx responses,
	// which don't need an artificial deadline to happen.

	function cacheKey(url) { return CACHE_PREFIX + url; }
	function readCache(url) {
		try {
			var raw = localStorage.getItem(cacheKey(url));
			return raw ? JSON.parse(raw) : null;
		} catch (e) { return null; }
	}
	function writeCache(url, html) {
		try {
			localStorage.setItem(cacheKey(url), JSON.stringify({ html: html, at: Date.now() }));
		} catch (e) { /* storage full or unavailable: just skip caching this one */ }
	}

	function ago(at) {
		var s = Math.round((Date.now() - at) / 1000);
		if (s < 60) return 'moments ago';
		var m = Math.round(s / 60);
		if (m < 60) return m + (m === 1 ? ' minute ago' : ' minutes ago');
		var h = Math.round(m / 60);
		return h + (h === 1 ? ' hour ago' : ' hours ago');
	}
	function staleBannerHTML(at) {
		return '<div style="background:#fef3c7;color:#92400e;border:1px solid #f59e0b;' +
			'border-radius:10px;padding:6px 10px;font:bold 12px sans-serif;margin-bottom:6px;">' +
			'⚠ Showing saved data from ' + ago(at) + ' — updating automatically</div>';
	}
	// For an outerHTML swap, the banner has to live INSIDE the same root element
	// (rather than next to it) so the element stays a single node with its
	// original id/data-team — otherwise a later retry, which replaces that node
	// wholesale, would leave the banner behind as an orphan once fresh content
	// lands beside it instead of over it.
	function withBannerInsideRoot(html, at) {
		var container = document.createElement('div');
		container.innerHTML = html;
		var root = container.firstElementChild;
		if (!root) return html; // not a single-root fragment; nothing safe to do
		var banner = document.createElement('div');
		banner.innerHTML = staleBannerHTML(at);
		root.insertBefore(banner.firstElementChild, root.firstChild);
		return root.outerHTML;
	}

	// A selector that will find this element again later, even after it's been
	// replaced by an outerHTML swap (so a retry can re-locate the live node).
	function selectorFor(el) {
		if (el.id) return '#' + el.id;
		var team = el.getAttribute('data-team');
		if (team) return '[data-team="' + team + '"]';
		return null;
	}

	var pendingRetries = []; // {selector, url, swapStyle}
	function retryAll() {
		var todo = pendingRetries;
		pendingRetries = [];
		todo.forEach(function (r) {
			if (!document.querySelector(r.selector)) return; // gone — nothing to refresh
			try {
				// target takes a CSS selector, not an element — htmx resolves it
				// itself at call time. Passing an element here instead silently
				// falls back to targeting <body>, replacing the whole page.
				htmx.ajax('GET', r.url, { target: r.selector, swap: r.swapStyle });
			} catch (e) { /* best effort */ }
		});
	}
	window.addEventListener('online', retryAll);
	setInterval(retryAll, RETRY_INTERVAL_MS);

	document.body.addEventListener('htmx:configRequest', function (evt) {
		if (evt.detail.verb !== 'get') return;
		var params = new URLSearchParams();
		Object.keys(evt.detail.parameters || {}).forEach(function (k) {
			params.append(k, evt.detail.parameters[k]);
		});
		var qs = params.toString();
		requestURLs.set(evt.detail.elt, evt.detail.path + (qs ? '?' + qs : ''));
	});

	document.body.addEventListener('htmx:afterRequest', function (evt) {
		var el = evt.detail.elt;
		var url = requestURLs.get(el);
		if (!url) return; // not a GET we're tracking

		if (evt.detail.successful) {
			writeCache(url, evt.detail.xhr.responseText);
			return;
		}

		var cached = readCache(url);
		if (!cached) return; // nothing to fall back to; leave htmx's own error state

		var swapStyle = (el.getAttribute('hx-swap') || 'innerHTML').split(' ')[0];
		if (swapStyle === 'outerHTML') {
			el.outerHTML = withBannerInsideRoot(cached.html, cached.at);
		} else {
			el.innerHTML = staleBannerHTML(cached.at) + cached.html;
		}

		var selector = selectorFor(el);
		if (selector) pendingRetries.push({ selector: selector, url: url, swapStyle: swapStyle });
	});
})();
