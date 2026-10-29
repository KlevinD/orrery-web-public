import { afterEach, describe, expect, it, vi } from "vitest";
import { clearApiBaseUrlForTests } from "#lib/runtime/api-base";
import { settingsApi } from "./settings";

describe("settingsApi", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    clearApiBaseUrlForTests();
  });

  it("posts PageIndex connection tests to the settings endpoint", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          status: "ok",
          api_url: "http://127.0.0.1:8091",
          endpoint: "http://127.0.0.1:8091/healthz",
          http_status: 200,
          latency_ms: 12,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.spyOn(crypto, "randomUUID").mockReturnValue(
      "00000000-0000-4000-8000-000000000003",
    );

    await settingsApi.testPageIndexConnection({
      pageindex: {
        api_url: "http://127.0.0.1:8091",
        api_key: "local-dev",
        timeout_seconds: 30,
      },
    });

    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/settings/knowledge/pageindex/test");
    expect(init?.method).toBe("POST");
    expect(init?.body).toBe(
      JSON.stringify({
        pageindex: {
          api_url: "http://127.0.0.1:8091",
          api_key: "local-dev",
          timeout_seconds: 30,
        },
      }),
    );
  });
});
