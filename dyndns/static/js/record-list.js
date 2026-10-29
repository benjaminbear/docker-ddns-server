(() => {
    const list = document.querySelector('.record-list');
    if (!list) return;
    const original = Array.from(list.querySelectorAll('form.record-card'));
    const buttons = Array.from(document.querySelectorAll('[data-record-sort]'));
    const reset = document.getElementById('record-sort-reset');
    const collator = new Intl.Collator(undefined, {numeric: true, sensitivity: 'base'});
    function clear() {
        buttons.forEach(button => {
            button.setAttribute('aria-pressed', 'false');
            delete button.dataset.direction;
        });
    }
    buttons.forEach(button => button.addEventListener('click', () => {
        const field = button.dataset.recordSort;
        const direction = button.dataset.direction === 'ascending' ? -1 : 1;
        clear();
        button.dataset.direction = direction === 1 ? 'ascending' : 'descending';
        button.setAttribute('aria-pressed', 'true');
        [...original].sort((a, b) => direction * (field === 'ttl'
            ? Number(a.dataset.ttl) - Number(b.dataset.ttl)
            : collator.compare(a.dataset[field], b.dataset[field])))
            .forEach(row => list.appendChild(row));
        reset.disabled = false;
    }));
    reset.addEventListener('click', () => {
        original.forEach(row => list.appendChild(row));
        clear();
        reset.disabled = true;
    });
})();
