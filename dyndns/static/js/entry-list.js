(() => {
    const table = document.querySelector('.entries-table');
    if (!table) return;
    const body = table.tBodies[0];
    const originalRows = Array.from(body.rows);
    const reset = document.querySelector('.entry-sort-reset');
    reset.addEventListener('click', () => {
        originalRows.filter(row => row.parentNode === body).forEach(row => body.appendChild(row));
        table.querySelectorAll('th[aria-sort]').forEach(th => th.setAttribute('aria-sort', 'none'));
        reset.disabled = true;
    });
    const zoneFilter = document.querySelector('.entry-zone-filter');
    const empty = document.querySelector('.entry-filter-empty');
    zoneFilter.addEventListener('change', () => {
        let visible = 0;
        Array.from(body.rows).forEach(row => {
            row.hidden = Boolean(zoneFilter.value && row.dataset.zone !== zoneFilter.value);
            if (!row.hidden) visible++;
        });
        empty.hidden = visible !== 0;
    });
    const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });

    // A fixed-width key orders addresses numerically, IPv4 before IPv6.
    function addressKey(address) {
        if (!address) return '';
        if (!address.includes(':')) {
            return '4:' + address.split('.').map(n => Number(n).toString(16).padStart(2, '0')).join('');
        }
        let value = address.toLowerCase();
        if (value.includes('.')) {
            const split = value.lastIndexOf(':');
            const bytes = value.slice(split + 1).split('.').map(Number);
            value = value.slice(0, split + 1) + ((bytes[0] << 8) | bytes[1]).toString(16)
                + ':' + ((bytes[2] << 8) | bytes[3]).toString(16);
        }
        const halves = value.split('::');
        const left = halves[0] ? halves[0].split(':') : [];
        const right = halves.length > 1 && halves[1] ? halves[1].split(':') : [];
        const groups = halves.length > 1
            ? [...left, ...Array(8 - left.length - right.length).fill('0'), ...right]
            : left;
        return '6:' + groups.map(n => n.padStart(4, '0')).join('');
    }

    table.querySelectorAll('.entry-sort').forEach(button => {
        button.addEventListener('click', () => {
            const header = button.closest('th');
            const ascending = header.getAttribute('aria-sort') !== 'ascending';
            const column = header.cellIndex;
            const type = button.dataset.sort;
            const rows = Array.from(body.rows);
            const value = row => row.cells[column].dataset.sortValue ?? row.cells[column].textContent.trim();
            rows.sort((a, b) => {
                let result;
                if (type === 'ttl' || type === 'updated') {
                    result = Number(value(a)) - Number(value(b));
                } else if (type === 'ip') {
                    const x = addressKey(value(a)), y = addressKey(value(b));
                    result = x < y ? -1 : x > y ? 1 : 0;
                } else {
                    result = collator.compare(value(a), value(b));
                }
                return ascending ? result : -result;
            });
            table.querySelectorAll('th[aria-sort]').forEach(th => th.setAttribute('aria-sort', 'none'));
            header.setAttribute('aria-sort', ascending ? 'ascending' : 'descending');
            rows.forEach(row => body.appendChild(row));
            reset.disabled = false;
        });
    });
})();
