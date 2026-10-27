import { inject, type Ref } from "vue";
import type { User } from "./api";
export type AppContext = {
  user: Ref<User | null>;
  people: Ref<User[]>;
  settings: Ref<any>;
  go: (page: string, id?: string, query?: string) => void;
  toast: (message: string, error?: boolean) => void;
  refresh: () => Promise<void>;
};
export const useApp = () => inject<AppContext>("app")!;
