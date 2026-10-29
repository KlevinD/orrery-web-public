import type { TranslationKey } from "./zh-CN";
import { enUSShell } from "./en-US/shell";
import { enUSHome } from "./en-US/home";
import { enUSMission } from "./en-US/mission";
import { enUSOperations } from "./en-US/operations";
import { enUSPeople } from "./en-US/people";
import { enUSResources } from "./en-US/resources";
import { enUSSettings } from "./en-US/settings";
import { enUSDevelopment } from "./en-US/development";
import { enUSMemoryCenter } from "./en-US/memory-center";
import { enUSResponsibility } from "./en-US/responsibility";
import { enUSRelationshipGraph } from "./en-US/relationship-graph";
import { enUSWorkCenter } from "./en-US/work-center";

export const enUS = {
  ...enUSShell,
  ...enUSHome,
  ...enUSMission,
  ...enUSOperations,
  ...enUSPeople,
  ...enUSResources,
  ...enUSSettings,
  ...enUSDevelopment,
  ...enUSMemoryCenter,
  ...enUSResponsibility,
  ...enUSRelationshipGraph,
  ...enUSWorkCenter,
} satisfies Record<TranslationKey, string>;
