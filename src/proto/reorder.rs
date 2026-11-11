use std::collections::BTreeMap;

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct PlaybackFrame {
    pub stream_id: u32,
    pub sequence: u32,
    pub payload: Vec<u8>,
}

pub struct ReorderBuffer {
    next: u32,
    limit: usize,
    pending: BTreeMap<u32, PlaybackFrame>,
}

impl ReorderBuffer {
    pub fn new(next: u32, limit: usize) -> Self {
        Self {
            next,
            limit,
            pending: BTreeMap::new(),
        }
    }

    pub fn next(&self) -> u32 {
        self.next
    }

    pub fn push(&mut self, frame: PlaybackFrame) -> (Vec<Vec<u8>>, bool) {
        if frame.sequence < self.next
            || self.pending.contains_key(&frame.sequence)
            || self.limit == 0
        {
            return (Vec::new(), false);
        }
        let mut ready = if self.pending.len() >= self.limit {
            self.skip_gap()
        } else {
            Vec::new()
        };
        if frame.sequence < self.next {
            return (ready, false);
        }
        self.pending.insert(frame.sequence, frame);
        self.drain(&mut ready);
        (ready, true)
    }

    pub fn skip_gap(&mut self) -> Vec<Vec<u8>> {
        let Some(sequence) = self
            .pending
            .range((self.next + 1)..)
            .next()
            .map(|(n, _)| *n)
        else {
            return Vec::new();
        };
        self.next = sequence;
        let mut ready = Vec::new();
        self.drain(&mut ready);
        ready
    }

    fn drain(&mut self, ready: &mut Vec<Vec<u8>>) {
        while let Some(frame) = self.pending.remove(&self.next) {
            ready.push(frame.payload);
            self.next = self.next.wrapping_add(1);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn frame(sequence: u32) -> PlaybackFrame {
        PlaybackFrame {
            stream_id: 7,
            sequence,
            payload: vec![sequence as u8],
        }
    }

    #[test]
    fn reorders_and_deduplicates_like_go() {
        let mut buffer = ReorderBuffer::new(1, 8);
        assert_eq!(buffer.push(frame(3)), (vec![], true));
        assert_eq!(buffer.push(frame(2)), (vec![], true));
        assert_eq!(
            buffer.push(frame(1)),
            (vec![vec![1], vec![2], vec![3]], true)
        );
        assert_eq!(buffer.push(frame(1)), (vec![], false));
        assert_eq!(buffer.next(), 4);
    }

    #[test]
    fn skips_to_earliest_pending_sequence() {
        let mut buffer = ReorderBuffer::new(10, 8);
        buffer.push(frame(12));
        buffer.push(frame(13));
        assert_eq!(buffer.skip_gap(), vec![vec![12], vec![13]]);
        assert_eq!(buffer.next(), 14);
    }
}
