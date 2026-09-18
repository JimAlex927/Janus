import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, getConfig, publishConfig, subscribeEvents, validateConfig } from "./api";
import { cloneConfig } from "./model";
import { validateLocal } from "./validate";
import type { JanusConfig } from "./types";

export type ConfigStatus = "loading" | "ready" | "unauthorized" | "error";

/**
 * 全局唯一的配置状态机。
 * 单一真相来源：只有 draft 可被编辑；snapshot 只在 load/publish 成功时替换。
 * 脏标记只增不清（除 load/publish 外），远端变更永不覆盖脏草稿。
 */
export function useConfig() {
  const [status, setStatus] = useState<ConfigStatus>("loading");
  const [revision, setRevision] = useState(0);
  const [draft, setDraft] = useState<JanusConfig | null>(null);
  const [dirty, setDirty] = useState(false);
  const [message, setMessage] = useState("");
  const [remoteChanged, setRemoteChanged] = useState(false);
  const [busy, setBusy] = useState(false);
  const dirtyRef = useRef(false);
  const statusRef = useRef<ConfigStatus>("loading");
  statusRef.current = status;

  const load = useCallback(async (force = false) => {
    if (dirtyRef.current && !force) {
      setMessage("远端配置已变化，但本地有未发布草稿，未自动覆盖。请发布或手动刷新。");
      setRemoteChanged(true);
      return;
    }
    setBusy(true);
    try {
      const snapshot = await getConfig();
      dirtyRef.current = false;
      setRevision(snapshot.revision);
      setDraft(snapshot.config);
      setDirty(false);
      setRemoteChanged(false);
      setStatus("ready");
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) setStatus("unauthorized");
      else {
        setStatus("error");
        setMessage(error instanceof Error ? error.message : String(error));
      }
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    load().catch(() => undefined);
    const close = subscribeEvents(() => {
      load().catch(() => undefined);
    });
    return close;
  }, [load]);

  const update = useCallback((fn: (prev: JanusConfig) => JanusConfig) => {
    setDraft((prev) => {
      if (!prev) return prev;
      return fn(cloneConfig(prev));
    });
    dirtyRef.current = true;
    setDirty(true);
    setRemoteChanged(false);
  }, []);

  const replace = useCallback((next: JanusConfig) => {
    setDraft(cloneConfig(next));
    dirtyRef.current = true;
    setDirty(true);
  }, []);

  const discard = useCallback(async () => {
    await load(true);
    setMessage("已丢弃本地草稿，回到远端版本。");
  }, [load]);

  const validate = useCallback(async (): Promise<boolean> => {
    if (!draft) return false;
    const localErrors = validateLocal(draft);
    if (localErrors.length > 0) {
      setMessage(`本地检查未通过：${localErrors[0]}`);
      return false;
    }
    setBusy(true);
    try {
      await validateConfig(draft);
      setMessage("配置校验通过。");
      return true;
    } catch (error) {
      setMessage(error instanceof Error ? error.message : String(error));
      return false;
    } finally {
      setBusy(false);
    }
  }, [draft]);

  const publish = useCallback(async (): Promise<boolean> => {
    if (!draft) return false;
    const localErrors = validateLocal(draft);
    if (localErrors.length > 0) {
      setMessage(`本地检查未通过：${localErrors[0]}`);
      return false;
    }
    setBusy(true);
    try {
      const result = await publishConfig(draft, revision);
      setRevision(result.revision);
      dirtyRef.current = false;
      setDirty(false);
      setRemoteChanged(false);
      setMessage(`已发布配置版本 ${result.revision}。`);
      await load(true);
      return true;
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        setMessage("版本冲突：远端已被他人更新。请刷新后合并再发布。");
        setRemoteChanged(true);
      } else {
        setMessage(error instanceof Error ? error.message : String(error));
      }
      return false;
    } finally {
      setBusy(false);
    }
  }, [draft, revision, load]);

  return {
    status,
    setStatus,
    revision,
    draft,
    dirty,
    busy,
    message,
    setMessage,
    remoteChanged,
    load,
    update,
    replace,
    discard,
    validate,
    publish,
  };
}

export type ConfigStore = ReturnType<typeof useConfig>;
