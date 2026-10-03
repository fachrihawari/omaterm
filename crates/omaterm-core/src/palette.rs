//! Pure deterministic palette result ordering (M16).

pub const MAX_PALETTE_RESULTS: usize = 100;

/// Matcher output supplied by a caller outside core. Core owns merge ordering,
/// successful-use recency, deterministic ties, and the final global cap.
#[derive(Debug, Clone, Copy)]
pub struct PaletteRank<'a> {
    pub class: u8,
    pub fuzzy_score: i64,
    pub mru_rank: Option<usize>,
    pub kind: u8,
    pub label: &'a str,
    pub key: &'a str,
}

pub fn rank_palette_indices(ranks: &[PaletteRank<'_>], limit: usize) -> Vec<usize> {
    let mut indices: Vec<_> = (0..ranks.len()).collect();
    indices.sort_by(|left, right| {
        let left = ranks[*left];
        let right = ranks[*right];
        left.class
            .cmp(&right.class)
            .then_with(|| right.fuzzy_score.cmp(&left.fuzzy_score))
            .then_with(|| {
                left.mru_rank
                    .unwrap_or(usize::MAX)
                    .cmp(&right.mru_rank.unwrap_or(usize::MAX))
            })
            .then_with(|| left.kind.cmp(&right.kind))
            .then_with(|| left.label.to_lowercase().cmp(&right.label.to_lowercase()))
            .then_with(|| left.key.cmp(right.key))
    });
    indices.truncate(limit.min(MAX_PALETTE_RESULTS));
    indices
}

#[cfg(test)]
mod tests {
    use super::*;

    fn rank(
        key: &'static str,
        label: &'static str,
        class: u8,
        mru: Option<usize>,
    ) -> PaletteRank<'static> {
        PaletteRank {
            class,
            fuzzy_score: 10,
            mru_rank: mru,
            kind: 0,
            label,
            key,
        }
    }

    #[test]
    fn exact_prefix_mru_and_stable_ties_are_ordered_purely() {
        let ranks = [
            rank("late", "same", 1, None),
            rank("fuzzy", "same", 2, Some(0)),
            rank("recent", "same", 1, Some(0)),
            rank("exact", "same", 0, None),
        ];
        assert_eq!(rank_palette_indices(&ranks, 10), [3, 2, 0, 1]);
    }

    #[test]
    fn global_limit_is_applied_to_ranked_indices() {
        let labels: Vec<_> = (0..MAX_PALETTE_RESULTS + 10)
            .map(|index| format!("item-{index:03}"))
            .collect();
        let ranks: Vec<_> = labels
            .iter()
            .enumerate()
            .map(|(index, label)| PaletteRank {
                class: 1,
                fuzzy_score: 1,
                mru_rank: None,
                kind: 0,
                label,
                key: if index == 0 { "000" } else { "other" },
            })
            .collect();
        assert_eq!(
            rank_palette_indices(&ranks, usize::MAX).len(),
            MAX_PALETTE_RESULTS
        );
    }
}
