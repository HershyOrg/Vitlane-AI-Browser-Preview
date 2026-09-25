import mockSafeAreaContext from "react-native-safe-area-context/jest/mock";

process.env.EXPO_PUBLIC_VITLANE_DATA_MODE = "fixture";

jest.mock("react-native-safe-area-context", () => mockSafeAreaContext);
jest.mock(
  "@react-native-async-storage/async-storage",
  () => require("@react-native-async-storage/async-storage/jest/async-storage-mock"),
);
