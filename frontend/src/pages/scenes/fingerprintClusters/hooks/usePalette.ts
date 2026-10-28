import { useCallback, useMemo } from "react";

import { CLUSTER_PALETTE } from "../../../../theme/palette";


const sceneColor = (sceneId: string, palette: Map<string, string>) => {
  const existing = palette.get(sceneId);
  if (existing) return existing;
  const c = CLUSTER_PALETTE[palette.size % CLUSTER_PALETTE.length];
  palette.set(sceneId, c);
  return c;
};

/**
 * Stable scene-id → color mapping. The seed scene is inserted first so it
 * always gets the same palette slot. The map is memoed because it's mutated
 * as new scene ids are encountered — a fresh map per render would reset.
 */
export const usePalette = (seedSceneId: string) => {
  const palette = useMemo(() => {
    const p = new Map<string, string>();
    sceneColor(seedSceneId, p);
    return p;
  }, [seedSceneId]);

  // Stable identity so downstream layout memos (ClusterCanvas) don't
  // invalidate on every parent re-render.
  return useCallback((id: string) => sceneColor(id, palette), [palette]);
};
