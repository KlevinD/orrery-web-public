// Copyright 2026 Minvia. SPDX-License-Identifier: Apache-2.0

/** Adapt a proxy's Negotiate challenge to the optional native Kerberos client. */
export function createNegotiateAuth() {
  return async ({ response, scheme }) => {
    if (scheme.toLowerCase() !== 'negotiate') {
      throw new Error(`Expected Negotiate scheme but got ${JSON.stringify(scheme)}`);
    }
    const raw = response.headers['proxy-authenticate'];
    const challenges = Array.isArray(raw) ? raw : [raw || ''];
    const challenge = challenges.map(value => /(?:^|,)\s*Negotiate(?:\s+([^\s,]+))?(?=\s*(?:,|$))/i.exec(value)).find(Boolean);
    if (!challenge) throw new Error('Proxy did not supply a Negotiate challenge');

    // Import only after an explicit challenge. Ordinary proxies need no native peer.
    const kerberos = await import('kerberos');
    const api = kerberos.default || kerberos;
    const client = await api.initializeClient('HTTP@proxy', { mechOID: api.GSS_MECH_OID_SPNEGO });
    const token = await client.step(challenge[1] || '');
    if (typeof token !== 'string' || !token) throw new Error('Kerberos returned an empty proxy authentication token');
    return { headers: { 'Proxy-Authorization': `Negotiate ${token}` } };
  };
}
