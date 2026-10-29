(function () {
    'use strict';
    var nav = document.querySelector('.nav-certs');
    if (nav) nav.classList.add('active');
    var pending = Array.from(document.querySelectorAll('[data-cert-pending]'));
    if (!pending.length) return;
    var delay = 2000;
    async function poll() {
        var controller = new AbortController();
        var timeout = setTimeout(function () { controller.abort(); }, 10000);
        try {
            var response = await fetch('/@/certs/status', {
                credentials: 'same-origin', cache: 'no-store', redirect: 'error',
                headers: { Accept: 'application/json' }, signal: controller.signal
            });
            if (!response.ok) throw new Error('Status unavailable');
            var rows = await response.json();
            if (!Array.isArray(rows)) throw new Error('Invalid status');
            var states = new Map(rows.map(function (row) { return [String(row.id), row.status]; }));
            var finished = pending.some(function (item) {
                var state = states.get(item.dataset.certPending);
                return ['valid', 'failed', 'revoked', 'expired'].includes(state);
            });
            if (finished) { if (window.refreshLists) { await window.refreshLists(); } else { window.location.reload(); } return; }
            pending.forEach(function (item) {
                item.querySelector('.cert-progress-text').textContent = 'Processing certificate — checking DNS and waiting for the certificate authority…';
            });
            delay = 2000;
        } catch (error) {
            pending.forEach(function (item) {
                item.querySelector('.cert-progress-text').textContent = 'Status connection interrupted. Reconnecting automatically; you can also use Refresh status.';
            });
            delay = Math.min(delay * 2, 15000);
        } finally {
            clearTimeout(timeout);
        }
        setTimeout(poll, delay);
    }
    setTimeout(poll, delay);
}());
