import { zhCNShell } from "./zh-CN/shell";
import { zhCNHome } from "./zh-CN/home";
import { zhCNMission } from "./zh-CN/mission";
import { zhCNOperations } from "./zh-CN/operations";
import { zhCNPeople } from "./zh-CN/people";
import { zhCNResources } from "./zh-CN/resources";
import { zhCNSettings } from "./zh-CN/settings";
import { zhCNDevelopment } from "./zh-CN/development";
import { zhCNMemoryCenter } from "./zh-CN/memory-center";
import { zhCNResponsibility } from "./zh-CN/responsibility";
import { zhCNRelationshipGraph } from "./zh-CN/relationship-graph";
import { zhCNWorkCenter } from "./zh-CN/work-center";

export const zhCN = {
  ...zhCNShell,
  ...zhCNHome,
  ...zhCNMission,
  ...zhCNOperations,
  ...zhCNPeople,
  ...zhCNResources,
  ...zhCNSettings,
  ...zhCNDevelopment,
  ...zhCNMemoryCenter,
  ...zhCNResponsibility,
  ...zhCNRelationshipGraph,
  ...zhCNWorkCenter,
} as const;

export type TranslationKey = keyof typeof zhCN;
