(() => {
    const message = document.querySelector('#passkey-message');
    const decode = value => Uint8Array.from(atob(value.replace(/-/g, '+').replace(/_/g, '/')), c => c.charCodeAt(0));
    const encode = value => btoa(String.fromCharCode(...new Uint8Array(value))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
    async function post(path, body = {}) {
        const response = await fetch(path, {method:'POST', credentials:'same-origin', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body)});
        const result = await response.json();
        if (!response.ok) throw new Error(result.message || 'The request failed. Please sign in again.');
        return result;
    }
    function credentialJSON(credential) {
        const r = credential.response;
        const response = {clientDataJSON:encode(r.clientDataJSON)};
        if (r.attestationObject) {
            response.attestationObject = encode(r.attestationObject);
            response.transports = r.getTransports ? r.getTransports() : [];
        } else {
            response.authenticatorData = encode(r.authenticatorData);
            response.signature = encode(r.signature);
            response.userHandle = r.userHandle ? encode(r.userHandle) : null;
        }
        return {id:credential.id, rawId:encode(credential.rawId), type:credential.type, response,
            authenticatorAttachment:credential.authenticatorAttachment, clientExtensionResults:credential.getClientExtensionResults()};
    }
    async function act(button, operation) {
        button.disabled = true;
        message.textContent = '';
        try {
            if (!window.PublicKeyCredential || !window.isSecureContext) throw new Error('Passkeys require HTTPS and a browser that supports them.');
            await operation();
        } catch (error) {
            message.textContent = error.name === 'NotAllowedError' ? 'Passkey request cancelled or timed out. Try again when ready.' : error.message;
        } finally { button.disabled = false; }
    }
    const login = document.querySelector('#passkey-login');
    if (login) login.addEventListener('click', () => act(login, async () => {
        if (location.origin !== login.dataset.origin) {
            location.assign(login.dataset.origin + '/@/login');
            return;
        }
        const {publicKey} = await post('/@/auth/passkey/login/begin');
        publicKey.challenge = decode(publicKey.challenge);
        if (publicKey.allowCredentials) publicKey.allowCredentials.forEach(c => c.id = decode(c.id));
        const credential = await navigator.credentials.get({publicKey});
        const result = await post('/@/auth/passkey/login/finish', credentialJSON(credential));
        location.assign(result.redirect);
    }));
    const register = document.querySelector('#passkey-register');
    if (register) register.addEventListener('submit', event => {
        event.preventDefault();
        act(register.querySelector('button'), async () => {
            const {publicKey} = await post('/@/auth/passkey/register/begin', {name:document.querySelector('#passkey-name').value});
            publicKey.challenge = decode(publicKey.challenge);
            publicKey.user.id = decode(publicKey.user.id);
            if (publicKey.excludeCredentials) publicKey.excludeCredentials.forEach(c => c.id = decode(c.id));
            const credential = await navigator.credentials.create({publicKey});
            await post('/@/auth/passkey/register/finish', credentialJSON(credential));
            location.reload();
        });
    });
    document.querySelectorAll('.passkey-revoke').forEach(button => button.addEventListener('click', () => {
        if (!confirm('Remove this passkey? Sessions signed in with it will also lose access.')) return;
        act(button, async () => { await post('/@/auth/passkey/' + encodeURIComponent(button.dataset.id) + '/revoke'); location.reload(); });
    }));
})();
