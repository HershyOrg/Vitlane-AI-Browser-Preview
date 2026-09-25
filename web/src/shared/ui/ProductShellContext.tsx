import {
  createContext,
  type ReactNode,
  useContext,
} from "react";

export type ProductShellCartRegistration = {
  curationId: string;
  count: number;
  onOpen: () => void;
};

type ProductShellContextValue = {
  registerCart: (
    registration: ProductShellCartRegistration,
  ) => () => void;
};

const ProductShellContext = createContext<ProductShellContextValue>({
  registerCart: () => () => undefined,
});

export function ProductShellProvider({
  children,
  registerCart,
}: ProductShellContextValue & { children: ReactNode }) {
  return (
    <ProductShellContext.Provider value={{ registerCart }}>
      {children}
    </ProductShellContext.Provider>
  );
}

export function useProductShellCart() {
  return useContext(ProductShellContext).registerCart;
}
