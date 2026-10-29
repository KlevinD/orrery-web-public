import type { IncomingHttpHeaders, OutgoingHttpHeaders } from 'node:http';
export interface ProxyAuthParams { response: { statusCode: number; headers: IncomingHttpHeaders }; scheme: string }
export interface ProxyAuthResponse { headers: OutgoingHttpHeaders }
export type OnProxyAuthCallback = (params: ProxyAuthParams) => Promise<ProxyAuthResponse>;
export function createNegotiateAuth(): OnProxyAuthCallback;
