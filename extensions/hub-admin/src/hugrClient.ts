/**
 * HugrClient — GraphQL client that communicates with Hugr servers via
 * hugr_connection_service proxy, handles multipart/mixed IPC responses
 * (JSON + Arrow), and manages authentication.
 *
 * Copied from hugr-kernel/extensions/jupyterlab/src/hugrClient.ts
 * TODO: extract into shared npm package @hugr-lab/hugr-client-js
 */
import { tableFromIPC } from 'apache-arrow';
import { PageConfig } from '@jupyterlab/coreutils';

export interface HugrClientOptions {
  /** Proxy URL: /hugr/proxy/{connectionName} */
  url: string;
  authType: 'public' | 'api_key' | 'bearer' | 'browser' | 'hub';
  apiKey?: string;
  apiKeyHeader?: string;
  token?: string;
  role?: string;
  connectionName?: string;
  timeout?: number;
}

export interface HugrResponse {
  data: Record<string, any>;
  errors: HugrError[];
  extensions: Record<string, any>;
}

export interface HugrError {
  message: string;
  path?: string[];
  extensions?: Record<string, any>;
}

interface MultipartPart {
  headers: Record<string, string>;
  body: Uint8Array;
}

function getXsrfToken(): string | undefined {
  const match = document.cookie
    .split(';')
    .map(c => c.trim())
    .find(c => c.startsWith('_xsrf='));
  return match ? decodeURIComponent(match.split('=')[1]) : undefined;
}

export function parseMultipart(
  buffer: ArrayBuffer,
  boundary: string
): MultipartPart[] {
  const raw = new Uint8Array(buffer);
  const decoder = new TextDecoder();
  const delimiter = new TextEncoder().encode(`--${boundary}`);
  const endMarker = new TextEncoder().encode(`--${boundary}--`);

  const parts: MultipartPart[] = [];

  const positions: number[] = [];
  for (let i = 0; i <= raw.length - delimiter.length; i++) {
    let match = true;
    for (let j = 0; j < delimiter.length; j++) {
      if (raw[i + j] !== delimiter[j]) {
        match = false;
        break;
      }
    }
    if (match) {
      positions.push(i);
    }
  }

  if (positions.length < 2) {
    return parts;
  }

  for (let p = 0; p < positions.length - 1; p++) {
    let start = positions[p] + delimiter.length;
    let isEnd = true;
    for (let j = 0; j < endMarker.length; j++) {
      if (raw[positions[p] + j] !== endMarker[j]) {
        isEnd = false;
        break;
      }
    }
    if (isEnd) break;

    if (raw[start] === 0x0d && raw[start + 1] === 0x0a) {
      start += 2;
    } else if (raw[start] === 0x0a) {
      start += 1;
    }

    const end = positions[p + 1];

    let partEnd = end;
    if (partEnd >= 2 && raw[partEnd - 2] === 0x0d && raw[partEnd - 1] === 0x0a) {
      partEnd -= 2;
    } else if (partEnd >= 1 && raw[partEnd - 1] === 0x0a) {
      partEnd -= 1;
    }

    const partBytes = raw.slice(start, partEnd);

    let headerEnd = -1;
    for (let i = 0; i < partBytes.length - 1; i++) {
      if (partBytes[i] === 0x0a && partBytes[i + 1] === 0x0a) {
        headerEnd = i;
        break;
      }
      if (
        i < partBytes.length - 3 &&
        partBytes[i] === 0x0d &&
        partBytes[i + 1] === 0x0a &&
        partBytes[i + 2] === 0x0d &&
        partBytes[i + 3] === 0x0a
      ) {
        headerEnd = i;
        break;
      }
    }

    if (headerEnd === -1) {
      parts.push({ headers: {}, body: partBytes });
      continue;
    }

    const headerText = decoder.decode(partBytes.slice(0, headerEnd));
    const headers: Record<string, string> = {};
    for (const line of headerText.split(/\r?\n/)) {
      const idx = line.indexOf(':');
      if (idx > 0) {
        headers[line.slice(0, idx).trim()] = line.slice(idx + 1).trim();
      }
    }

    let bodyStart = headerEnd + 2;
    if (headerEnd < partBytes.length - 3 && partBytes[headerEnd] === 0x0d) {
      bodyStart = headerEnd + 4;
    }

    parts.push({ headers, body: partBytes.slice(bodyStart) });
  }

  return parts;
}

function setNested(target: Record<string, any>, path: string, value: any): void {
  const segments = path.split('.');
  let current = target;
  for (let i = 0; i < segments.length - 1; i++) {
    const seg = segments[i];
    if (!(seg in current) || typeof current[seg] !== 'object') {
      current[seg] = {};
    }
    current = current[seg];
  }
  current[segments[segments.length - 1]] = value;
}

export class HugrClient {
  private _url: string;
  private _authType: 'public' | 'api_key' | 'bearer' | 'browser' | 'hub';
  private _apiKey?: string;
  private _apiKeyHeader: string;
  private _token?: string;
  private _role?: string;
  private _timeout: number;
  private _controllers: Set<AbortController> = new Set();
  private _connectionName?: string;
  private _cachedBrowserToken?: { access_token: string; expires_at: number };

  constructor(options: HugrClientOptions) {
    this._url = options.url;
    this._authType = options.authType;
    this._apiKey = options.apiKey;
    this._apiKeyHeader = options.apiKeyHeader || 'X-Api-Key';
    this._token = options.token;
    this._role = options.role;
    this._connectionName = options.connectionName;
    this._timeout = options.timeout ?? 30000;
  }

  private async _fetchBrowserToken(): Promise<string | undefined> {
    if (!this._connectionName) return undefined;

    if (this._cachedBrowserToken) {
      const ttl = this._cachedBrowserToken.expires_at - Date.now() / 1000;
      if (ttl > 30) return this._cachedBrowserToken.access_token;
    }

    const headers: Record<string, string> = {};
    const xsrf = getXsrfToken();
    if (xsrf) headers['X-XSRFToken'] = xsrf;

    try {
      const resp = await fetch(
        `${PageConfig.getBaseUrl()}hugr/connections/${encodeURIComponent(this._connectionName)}/token`,
        { headers, credentials: 'same-origin' }
      );
      if (!resp.ok) return undefined;
      const data = await resp.json();
      this._cachedBrowserToken = { access_token: data.access_token, expires_at: data.expires_at };
      return data.access_token;
    } catch {
      return undefined;
    }
  }

  abort(): void {
    for (const c of this._controllers) c.abort();
    this._controllers.clear();
  }

  async query(graphql: string, variables?: Record<string, any>): Promise<HugrResponse> {
    const controller = new AbortController();
    this._controllers.add(controller);
    const timeoutId = setTimeout(() => controller.abort(), this._timeout);

    try {
      const headers: Record<string, string> = { 'Content-Type': 'application/json' };

      if (this._authType === 'browser' || this._authType === 'hub') {
        const browserToken = await this._fetchBrowserToken();
        if (browserToken) headers['Authorization'] = `Bearer ${browserToken}`;
      } else if (this._authType === 'api_key' && this._apiKey) {
        headers[this._apiKeyHeader] = this._apiKey;
      } else if (this._authType === 'bearer' && this._token) {
        headers['Authorization'] = `Bearer ${this._token}`;
      }

      if (this._role) headers['X-Hugr-Role'] = this._role;
      const xsrf = getXsrfToken();
      if (xsrf) headers['X-XSRFToken'] = xsrf;

      const body: Record<string, any> = { query: graphql };
      if (variables) body.variables = variables;

      const response = await fetch(this._url, {
        method: 'POST',
        headers,
        body: JSON.stringify(body),
        signal: controller.signal,
        credentials: 'same-origin',
      });

      const contentType = response.headers.get('Content-Type') || '';

      if (!response.ok) {
        // Retry once on 401 — connection_service may be refreshing token
        if (response.status === 401 && !body._retried) {
          await new Promise(r => setTimeout(r, 3000));
          body._retried = true;
          const retryResp = await fetch(this._url, {
            method: 'POST',
            headers,
            body: JSON.stringify(body),
            signal: controller.signal,
            credentials: 'same-origin',
          });
          if (retryResp.ok) {
            const ct = retryResp.headers.get('Content-Type') || '';
            if (ct.includes('multipart/mixed')) {
              return this._parseMultipartResponse(await retryResp.arrayBuffer());
            }
            const json = await retryResp.json();
            return { data: json.data ?? {}, errors: json.errors ?? [], extensions: json.extensions ?? {} };
          }
        }
        const msg = response.status === 401
          ? 'Authentication failed — token expired or missing.'
          : response.status === 403
            ? 'Access denied — insufficient permissions.'
            : `Server error: ${response.status} ${response.statusText}`;
        return { data: {}, errors: [{ message: msg }], extensions: {} };
      }

      if (contentType.includes('multipart/mixed')) {
        return this._parseMultipartResponse(await response.arrayBuffer());
      }

      const json = await response.json();
      return { data: json.data ?? {}, errors: json.errors ?? [], extensions: json.extensions ?? {} };
    } finally {
      clearTimeout(timeoutId);
      this._controllers.delete(controller);
    }
  }

  private _parseMultipartResponse(buffer: ArrayBuffer): HugrResponse {
    const parts = parseMultipart(buffer, 'HUGR');
    const decoder = new TextDecoder();
    const result: HugrResponse = { data: {}, errors: [], extensions: {} };

    for (const part of parts) {
      const partType = part.headers['X-Hugr-Part-Type'];
      const format = part.headers['X-Hugr-Format'];

      switch (partType) {
        case 'data': {
          const path = part.headers['X-Hugr-Path'];
          let parsed: any;
          if (format === 'table') {
            const table = tableFromIPC(part.body);
            parsed = table.toArray().map((row: any) => {
              if (typeof row.toJSON === 'function') return row.toJSON();
              const obj: Record<string, any> = {};
              for (const field of table.schema.fields) obj[field.name] = row[field.name];
              return obj;
            });
          } else {
            parsed = JSON.parse(decoder.decode(part.body));
          }
          if (path) setNested(result, path, parsed);
          else Object.assign(result.data, parsed);
          break;
        }
        case 'errors': {
          const errs: HugrError[] = JSON.parse(decoder.decode(part.body));
          result.errors.push(...errs);
          break;
        }
        case 'extensions': {
          Object.assign(result.extensions, JSON.parse(decoder.decode(part.body)));
          break;
        }
        default: {
          try {
            Object.assign(result.data, JSON.parse(decoder.decode(part.body)));
          } catch { /* skip */ }
          break;
        }
      }
    }

    return result;
  }
}
