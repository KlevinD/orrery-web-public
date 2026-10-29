// Copyright 2026 Minvia. SPDX-License-Identifier: Apache-2.0
export const state = { calls: [], token: 'test-client-token', error: null };
export const GSS_MECH_OID_SPNEGO = 'test-spnego';
export async function initializeClient(...args) {
  state.calls.push(['initializeClient', ...args]);
  if (state.error) throw state.error;
  return { async step(challenge) { state.calls.push(['step', challenge]); return state.token; } };
}
export default { initializeClient, GSS_MECH_OID_SPNEGO };
