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
  const editVersion = useRef(0);
  const loadVersion = useRef(0);
  const publishing = useRef(false);
  const pending = useRef(0);
  const begin = () => { pending.current++; setBusy(true); };
  const end = () => { pending.current--; setBusy(pending.current > 0); };
  const statusRef = useRef<ConfigStatus>("loading");
  statusRef.current = status;

  const load = useCallback(async (force = false) => {
    if (publishing.current) return false;
    if (dirtyRef.current && !force) {
      setMessage("远端配置已变化，但本地有未发布草稿，未自动覆盖。请发布或手动刷新。");
      setRemoteChanged(true);
      return false;
    }
    const requestVersion = ++loadVersion.current;
    const editedAtStart = editVersion.current;
    begin();
    try {
      const snapshot = await getConfig();
      if (requestVersion !== loadVersion.current) return false;
      if (editedAtStart !== editVersion.current) {
        setRemoteChanged(true);
        setMessage("加载期间产生了新编辑，已保留本地草稿；请确认远端变化后合并。");
        return false;
      }
      dirtyRef.current = false;
      setRevision(snapshot.revision);
      setDraft(snapshot.config);
      setDirty(false);
      setRemoteChanged(false);
      setStatus("ready");
      return true;
    } catch (error) {
      if (requestVersion !== loadVersion.current) return false;
      if (error instanceof ApiError && error.status === 401) setStatus("unauthorized");
      else {
        if (statusRef.current !== "ready") setStatus("error");
        setMessage(error instanceof Error ? error.message : String(error));
      }
      return false;
    } finally {
      end();
    }
  }, []);

  useEffect(() => {
    load().catch(() => undefined);
    const close = subscribeEvents(() => {
      load().catch(() => undefined);
    });
    return () => { ++loadVersion.current; close(); };
  }, [load]);

  const update = useCallback((fn: (prev: JanusConfig) => JanusConfig) => {
    ++editVersion.current;
    setDraft((prev) => {
      if (!prev) return prev;
      return fn(cloneConfig(prev));
    });
    dirtyRef.current = true;
    setDirty(true);
    setRemoteChanged(false);
  }, []);

  const replace = useCallback((next: JanusConfig) => {
    ++editVersion.current;
    setDraft(cloneConfig(next));
    dirtyRef.current = true;
    setDirty(true);
  }, []);

  const discard = useCallback(async () => {
    if (await load(true)) setMessage("已丢弃本地草稿，回到远端版本。");
  }, [load]);

  const validate = useCallback(async (): Promise<boolean> => {
    if (!draft) return false;
    const localErrors = validateLocal(draft);
    if (localErrors.length > 0) {
      setMessage(`本地检查未通过：${localErrors[0]}`);
      return false;
    }
    begin();
    try {
      await validateConfig(draft);
      setMessage("配置校验通过。");
      return true;
    } catch (error) {
      setMessage(error instanceof Error ? error.message : String(error));
      return false;
    } finally {
      end();
    }
  }, [draft]);

  const publish = useCallback(async (): Promise<boolean> => {
    if (!draft || publishing.current) return false;
    const localErrors = validateLocal(draft);
    if (localErrors.length > 0) {
      setMessage(`本地检查未通过：${localErrors[0]}`);
      return false;
    }
    publishing.current = true;
    ++loadVersion.current;
    const submittedEdit = editVersion.current;
    const submitted = cloneConfig(draft);
    begin();
    try {
      const result = await publishConfig(submitted, revision);
      setRevision(result.revision);
      const edited = submittedEdit !== editVersion.current;
      dirtyRef.current = edited;
      setDirty(edited);
      setRemoteChanged(false);
      setMessage(`已发布配置版本 ${result.revision}。${edited ? "发布期间的新编辑已保留，尚未发布。" : ""}`);
      // Do not replace local edits with a post-publication fetch. The next
      // remote event can refresh an unedited snapshot normally.
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
      publishing.current = false;
      end();
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
