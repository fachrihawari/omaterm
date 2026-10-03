use uuid::Uuid;

macro_rules! typed_id {
    ($name:ident) => {
        #[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
        pub struct $name(pub Uuid);

        impl Default for $name {
            fn default() -> Self {
                Self::new()
            }
        }

        impl $name {
            pub fn new() -> Self {
                Self(Uuid::new_v4())
            }
        }
    };
}

typed_id!(PaneId);
typed_id!(SplitId);
typed_id!(DocumentId);
typed_id!(SessionId);
typed_id!(WindowId);
typed_id!(ProjectId);
typed_id!(TabId);
