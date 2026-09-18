import test from "node:test";
import assert from "node:assert/strict";
import { simulateRequest } from "./simulate.ts";

test("simulation selects the highest-priority matching route", () => {
  const result = simulateRequest({
    routes: [
      { name: "fallback", path_prefix: "/", service: "default" },
      { name: "api", match: "Host(`api.example.com`) && PathPrefix(`/api`) && Method(`GET`)", priority: 10, middlewares: ["auth"], action: { forward: { service: "api" } } },
    ],
  }, { target: "https://api.example.com/api/users", method: "GET", protocol: "http" });

  assert.equal(result.matchedRoute?.name, "api");
  assert.equal(result.matchedRoute?.service, "api");
  assert.deepEqual(result.matchedRoute?.middlewares, ["auth"]);
});

test("simulation respects Protocol rules and path-prefix boundaries", () => {
  const config = { routes: [{ name: "events", match: "PathPrefix(`/events`) && Protocol(`sse`)", service: "events" }] };

  assert.equal(simulateRequest(config, { target: "/events/1", method: "GET", protocol: "sse" }).matchedRoute?.name, "events");
  assert.equal(simulateRequest(config, { target: "/events-extra", method: "GET", protocol: "sse" }).matchedRoute, undefined);
  assert.equal(simulateRequest(config, { target: "/events/1", method: "GET", protocol: "http" }).matchedRoute, undefined);
});

test("simulation treats a match without Protocol as protocol-agnostic", () => {
  const config = { routes: [{ name: "api", path_prefix: "/api", service: "api" }] };

  assert.equal(simulateRequest(config, { target: "/api", method: "GET", protocol: "http" }).matchedRoute?.name, "api");
  assert.equal(simulateRequest(config, { target: "/api", method: "GET", protocol: "sse" }).matchedRoute?.name, "api");
});

test("simulation reports unsupported custom rules instead of guessing", () => {
  const result = simulateRequest({ routes: [{ name: "custom", match: "Tenant(`acme`)", service: "api" }] }, { target: "/", method: "GET", protocol: "http" });

  assert.equal(result.matchedRoute, undefined);
  assert.match(result.candidates[0].reason, /不支持自定义规则/);
});

test("simulation accepts an explicit host and headers for rule evaluation", () => {
  const result = simulateRequest({ routes: [{ name: "tenant", match: "Host(`api.example.com`) && Header(`X-Tenant`, `acme`)", service: "api" }] }, {
    target: "/api",
    host: "api.example.com",
    headers: { "X-Tenant": "acme" },
    method: "GET",
    protocol: "http",
  });

  assert.equal(result.matchedRoute?.name, "tenant");
});
