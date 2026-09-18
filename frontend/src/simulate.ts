import type { JanusConfig as Config } from "./types";

export type SimulationRequest = {
  target: string;
  method: string;
  protocol: string;
  host?: string;
  limen?: string;
  headers?: Record<string, string>;
};

export type SimulationCandidate = {
  routeName: string;
  matched: boolean;
  reason: string;
};

export type SimulationResult = {
  request: { method: string; protocol: string; host: string; path: string; limen: string };
  matchedRoute?: { name: string; service: string; middlewares: string[]; action: string };
  candidates: SimulationCandidate[];
  error?: string;
};

type Facts = { host: string; path: string; method: string; protocol: string; headers: Record<string, string>; query: URLSearchParams };
type Predicate = (facts: Facts) => boolean;
type Token = { kind: "word" | "value" | "and" | "or" | "not" | "left" | "right" | "comma" | "end"; text: string };

// This preview intentionally mirrors the built-in rules registry. Custom
// server-side rules remain visible as unsupported instead of being guessed.
export function simulateRequest(config: Config, request: SimulationRequest): SimulationResult {
  const parsed = parseTarget(request.target, request.host);
  if (parsed.error) return { request: { method: request.method, protocol: request.protocol, host: "", path: "", limen: request.limen || "" }, candidates: [], error: parsed.error };
  const facts: Facts = { host: parsed.host, path: parsed.path, method: request.method.toUpperCase() || "GET", protocol: request.protocol || "http", headers: lowerHeaders(request.headers), query: parsed.query };
  const normalized = { method: facts.method, protocol: facts.protocol, host: facts.host, path: facts.path, limen: request.limen || "" };
  const candidates: SimulationCandidate[] = [];
  const matched: { route: any; index: number }[] = [];

  (config.routes || []).forEach((route, index) => {
    const result = evaluateRoute(route, facts, request.limen || "");
    candidates.push({ routeName: route.name || `route-${index + 1}`, matched: result.matched, reason: result.reason });
    if (result.matched) matched.push({ route, index });
  });
  matched.sort((a, b) => routeScore(b.route, facts) - routeScore(a.route, facts) || a.index - b.index);
  const winner = matched[0]?.route;
  if (!winner) return { request: normalized, candidates };
  const winnerIndex = matched[0]?.index ?? 0;
  const action = winner.action || {};
  return {
    request: normalized,
    candidates,
    matchedRoute: {
      name: winner.name || `route-${winnerIndex + 1}`,
      service: action.forward?.service || winner.service || "",
      middlewares: winner.middlewares || [],
      action: action.redirect ? "Redirect" : action.respond ? "Direct response" : "Forward",
    },
  };
}

function parseTarget(target: string, explicitHost?: string): { host: string; path: string; query: URLSearchParams; error?: string } {
  const value = target.trim();
  if (!value) return { host: "", path: "", query: new URLSearchParams(), error: "请输入请求 URL 或路径" };
  try {
    const relative = value.startsWith("/");
    const url = new URL(value, "http://janus.local");
    return { host: (explicitHost || (relative ? "" : url.hostname)).toLowerCase().replace(/\.$/, ""), path: url.pathname || "/", query: url.searchParams };
  } catch {
    return { host: "", path: "", query: new URLSearchParams(), error: "请求 URL 无法解析" };
  }
}

function evaluateRoute(route: any, facts: Facts, limen: string): { matched: boolean; reason: string } {
  if (route.limen && limen && route.limen !== limen) return { matched: false, reason: `Limen 不匹配（需要 ${route.limen}）` };
  try {
    if (route.match) {
      const matcher = compileExpression(route.match);
      return matcher(facts) ? { matched: true, reason: "Match 规则通过" } : { matched: false, reason: "Match 规则未通过" };
    }
    if (route.host && route.host.toLowerCase().replace(/\.$/, "") !== facts.host) return { matched: false, reason: "Host 不匹配" };
    const prefix = route.path_prefix || "/";
    if (!(prefix === "/" || facts.path === prefix || facts.path.startsWith(`${prefix}/`))) return { matched: false, reason: "PathPrefix 不匹配" };
    return { matched: true, reason: "结构化规则通过" };
  } catch (error) {
    return { matched: false, reason: error instanceof Error ? error.message : "规则无法在浏览器中模拟" };
  }
}

function routeScore(route: any, facts: Facts): number {
  const host = route.host || (/Host\s*\(/i.test(route.match || "") ? 1 : 0);
  const path = route.path_prefix?.length || expressionPathLength(route.match || "");
  return Number(route.priority || 0) * 1_000_000 + host * 100_000 + path;
}

function expressionPathLength(expression: string): number {
  const match = expression.match(/(?:Path|PathPrefix)\s*\(\s*[`"']([^`"']*)[`"']\s*\)/i);
  return match?.[1]?.length || 0;
}

function lowerHeaders(headers?: Record<string, string>): Record<string, string> {
  return Object.fromEntries(Object.entries(headers || {}).map(([name, value]) => [name.toLowerCase(), value]));
}

function compileExpression(expression: string): Predicate {
  const parser = new ExpressionParser(tokenize(expression));
  const matcher = parser.parseExpression();
  parser.expect("end");
  return matcher;
}

function tokenize(expression: string): Token[] {
  const tokens: Token[] = [];
  let index = 0;
  while (index < expression.length) {
    const char = expression[index];
    if (/\s/.test(char)) { index += 1; continue; }
    if (expression.startsWith("&&", index)) { tokens.push({ kind: "and", text: "&&" }); index += 2; continue; }
    if (expression.startsWith("||", index)) { tokens.push({ kind: "or", text: "||" }); index += 2; continue; }
    if (char === "!") { tokens.push({ kind: "not", text: char }); index += 1; continue; }
    if (char === "(") { tokens.push({ kind: "left", text: char }); index += 1; continue; }
    if (char === ")") { tokens.push({ kind: "right", text: char }); index += 1; continue; }
    if (char === ",") { tokens.push({ kind: "comma", text: char }); index += 1; continue; }
    if (char === "`" || char === "\"" || char === "'") {
      const quote = char;
      let value = "";
      index += 1;
      while (index < expression.length && expression[index] !== quote) {
        if (expression[index] === "\\" && index + 1 < expression.length) index += 1;
        value += expression[index++];
      }
      if (expression[index] !== quote) throw new Error("字符串缺少结束引号");
      index += 1;
      tokens.push({ kind: "value", text: value });
      continue;
    }
    const start = index;
    while (index < expression.length && !/[\s(),!&|]/.test(expression[index])) index += 1;
    tokens.push({ kind: "word", text: expression.slice(start, index) });
  }
  tokens.push({ kind: "end", text: "" });
  return tokens;
}

class ExpressionParser {
  private index = 0;
  private readonly tokens: Token[];
  constructor(tokens: Token[]) { this.tokens = tokens; }
  parseExpression(): Predicate { return this.parseOr(); }
  private parseOr(): Predicate { let left = this.parseAnd(); while (this.take("or")) { const right = this.parseAnd(); const previous = left; left = facts => previous(facts) || right(facts); } return left; }
  private parseAnd(): Predicate { let left = this.parseUnary(); while (this.take("and")) { const right = this.parseUnary(); const previous = left; left = facts => previous(facts) && right(facts); } return left; }
  private parseUnary(): Predicate { if (this.take("not")) { const child = this.parseUnary(); return facts => !child(facts); } if (this.take("left")) { const child = this.parseExpression(); this.expect("right"); return child; } return this.parsePredicate(); }
  private parsePredicate(): Predicate { const name = this.consume("word").text; this.expect("left"); const args: string[] = []; if (!this.peek("right")) { do { args.push(this.consume(this.peek("value") ? "value" : "word").text); } while (this.take("comma")); } this.expect("right"); return predicate(name, args); }
  private take(kind: Token["kind"]): boolean { if (this.peek(kind)) { this.index += 1; return true; } return false; }
  expect(kind: Token["kind"]): void { if (!this.take(kind)) throw new Error(`规则语法错误：需要 ${kind}`); }
  private consume(kind: Token["kind"]): Token { if (!this.peek(kind)) throw new Error(`规则语法错误：需要 ${kind}`); return this.tokens[this.index++]; }
  private peek(kind: Token["kind"]): boolean { return this.tokens[this.index].kind === kind; }
}

function predicate(name: string, args: string[]): Predicate {
  const normalized = name.toLowerCase();
  if (normalized === "host") return facts => args.some(value => value.toLowerCase().replace(/\.$/, "") === facts.host);
  if (normalized === "path") return facts => args.length === 1 && facts.path === args[0];
  if (normalized === "pathprefix") return facts => args.length === 1 && (args[0] === "/" || facts.path === args[0] || facts.path.startsWith(`${args[0]}/`));
  if (normalized === "method") return facts => args.some(value => value.toUpperCase() === facts.method);
  if (normalized === "protocol") return facts => args.some(value => value.toLowerCase() === facts.protocol.toLowerCase());
  if (normalized === "header") return facts => { const name = args[0]?.toLowerCase(); if (!name || !(name in facts.headers)) return false; return args.length === 1 || facts.headers[name] === args[1]; };
  if (normalized === "query") return facts => { const value = facts.query.get(args[0] || ""); return value !== null && (args.length === 1 || value === args[1]); };
  throw new Error(`浏览器模拟不支持自定义规则 ${name}`);
}
